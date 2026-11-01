package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

var receiptDate = regexp.MustCompile(`\b([0-3][0-9])[./]([01][0-9])[./]((?:20)?[0-9]{2})\b`)
var receiptTotal = regexp.MustCompile(`(?im)(?:summe|gesamt|total|betrag)\s*[:=]?\s*(?:EUR|€)?\s*([0-9]{1,8}[,.][0-9]{2})`)
var receiptQuantity = regexp.MustCompile(`(?im)([0-9]{1,5}[,.][0-9]{1,3})\s*(?:liter|l|kg)\b`)

// ProposeCapture is intentionally conservative. OCR text is untrusted source data.
func ProposeCapture(module, text string) store.Record {
	out := store.Record{}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	title := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			title = line
			break
		}
	}
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	if module == "recipes" || module == "documents" {
		out["title"] = title
	}
	if module == "expenses" {
		out["description"] = title
	}
	if module == "fuel" {
		out["station"] = title
	}
	if module == "fuel" || module == "expenses" {
		if parts := receiptDate.FindStringSubmatch(text); parts != nil {
			year := parts[3]
			if len(year) == 2 {
				year = "20" + year
			}
			date := year + "-" + parts[2] + "-" + parts[1]
			if value.Date(date) {
				out["occurred_on"] = date
			}
		}
		if parts := receiptTotal.FindStringSubmatch(text); parts != nil {
			if _, err := value.Minor(parts[1]); err == nil {
				field := "amount_minor"
				if module == "fuel" {
					field = "total_minor"
				}
				out[field] = parts[1]
			}
		}
	}
	if module == "fuel" {
		if parts := receiptQuantity.FindStringSubmatch(text); parts != nil {
			out["quantity"] = parts[1]
		}
		if strings.Contains(strings.ToLower(text), "diesel") {
			out["fuel_type"] = "diesel"
		}
	}
	return out
}

type cappedOutput struct{ buffer bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > 1<<20-b.buffer.Len() {
		return 0, errors.New("OCR output too large")
	}
	return b.buffer.Write(p)
}

func (b *cappedOutput) String() string { return b.buffer.String() }
func (w *Worker) capture(ctx context.Context, job, lease string) error {
	ctx = audit.WithSystem(ctx, "worker")
	if !w.Cfg.OCREnabled {
		tx, err := audit.Begin(ctx, w.Pool)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
			return err
		}
		var captureID string
		err = tx.QueryRow(ctx, `SELECT c.id FROM core.capture_jobs c JOIN core.jobs j ON c.id::text=j.payload->>'capture_id'
 WHERE j.id=$1 AND j.lease_token=$2 AND j.state='running' AND j.lease_until>now() AND c.state='extracting' FOR UPDATE OF c`, job, lease).Scan(&captureID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if captureID != "" {
			if err = store.CheckFinanceArtifactJob(ctx, tx, job, lease); err != nil {
				return err
			}
			operation, prepareErr := store.PrepareFinanceArtifactJob(ctx, tx, job, lease, "capture", captureID)
			if prepareErr != nil {
				return prepareErr
			}
			if _, err = tx.Exec(ctx, `UPDATE core.capture_jobs SET state='review',source_text='Lokale Texterkennung ist deaktiviert. Werte bitte manuell prüfen.' WHERE id=$1`, captureID); err != nil {
				return err
			}
			if err = store.CompleteFinanceArtifactJob(ctx, tx, operation); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE core.jobs j SET state='paused',lease_until=NULL,lease_token=NULL
			FROM core.module_states m WHERE j.id=$1 AND j.lease_token=$2 AND j.state='running' AND j.lease_until>now()
			AND m.household_id=j.household_id AND m.module_id=j.module_id AND m.state='enabled' AND m.data_generation=j.module_generation`, job, lease)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if err = reviewCaptures(ctx, tx, &job, "paused", "Lokale Texterkennung ist deaktiviert. Werte bitte manuell prüfen."); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var capture, key, mid, hid string
	var generation int64
	err := w.Pool.QueryRow(ctx, "SELECT j.payload->>'capture_id',j.payload->>'storage_key',j.module_id,j.household_id,j.module_generation FROM core.jobs j WHERE j.id=$1 AND j.lease_token=$2", job, lease).Scan(&capture, &key, &mid, &hid, &generation)
	if err != nil {
		return err
	}
	if !value.ValidUUID(key) || !value.ValidUUID(capture) {
		return errors.New("invalid capture job")
	}
	if err = w.financeArtifactPreflight(ctx, job, lease); err != nil {
		return err
	}
	root, err := os.OpenRoot(w.Cfg.UploadDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(key)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	ocrCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ocrCtx, "/usr/bin/tesseract", "stdin", "stdout", "-l", "deu+eng", "--psm", "6")
	cmd.Stdin = file
	var output cappedOutput
	cmd.Stdout = &output
	cmd.Stderr = &cappedOutput{}
	ocrErr := cmd.Run()
	text := strings.ToValidUTF8(output.String(), "")
	if len([]rune(text)) > 20000 {
		text = string([]rune(text)[:20000])
	}
	proposal := ProposeCapture(mid, text)
	if ocrErr != nil {
		proposal = store.Record{}
		text = "Lokale Texterkennung fehlgeschlagen. Bitte Werte manuell am Original erfassen."
	}
	raw, err := json.Marshal(proposal)
	if err != nil {
		return err
	}
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", hid); err != nil {
		return err
	}
	var active bool
	err = tx.QueryRow(ctx, "SELECT true FROM core.jobs j JOIN core.module_states m ON m.household_id=j.household_id AND m.module_id=j.module_id WHERE j.id=$1 AND j.lease_token=$2 AND j.lease_until>now() AND m.state='enabled' AND m.data_generation=$3 FOR UPDATE OF j FOR SHARE OF m", job, lease, generation).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = store.CheckFinanceArtifactJob(ctx, tx, job, lease); err != nil {
		return err
	}
	operation, err := store.PrepareFinanceArtifactJob(ctx, tx, job, lease, "capture", capture)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.capture_jobs SET extracted_values=$2,source_text=$3,confidence='{\"requires_review\":true}',state='review' WHERE id=$1 AND state='extracting'", capture, raw, text); err != nil {
		return err
	}
	if err = store.CompleteFinanceArtifactJob(ctx, tx, operation); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.jobs SET state='done',finished_at=now(),lease_until=NULL,lease_token=NULL WHERE id=$1 AND lease_token=$2", job, lease); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *Worker) files(ctx context.Context) error {
	ctx = audit.WithSystem(ctx, "worker")
	if err := os.MkdirAll(w.Cfg.UploadDir, 0750); err != nil {
		return err
	}
	root, err := os.OpenRoot(w.Cfg.UploadDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT storage_key,attachment_id FROM core.file_deletions WHERE available_at<=now() ORDER BY available_at LIMIT 20 FOR UPDATE SKIP LOCKED")
	if err != nil {
		return err
	}
	type item struct{ key, id string }
	items := []item{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.key, &x.id); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range items {
		if !value.ValidUUID(x.key) {
			return errors.New("invalid file tombstone")
		}
		var retired bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.attachments a WHERE a.id=$1 AND a.state IN('purged','orphaned')
 AND(a.storage_key=$2 OR EXISTS(SELECT 1 FROM core.attachment_derivatives d WHERE d.attachment_id=a.id AND d.storage_key=$2)))
 OR(NOT EXISTS(SELECT 1 FROM core.attachments WHERE id=$1)
 AND NOT EXISTS(SELECT 1 FROM core.attachments WHERE storage_key=$2)
 AND NOT EXISTS(SELECT 1 FROM core.attachment_derivatives WHERE storage_key=$2))`, x.id, x.key).Scan(&retired)
		if err != nil {
			return err
		}
		if !retired {
			return errors.New("file deletion source is still referenced or not retired")
		}
		err = root.Remove(x.key)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM core.capture_jobs WHERE source_attachment_id=$1", x.id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM core.attachment_derivatives WHERE attachment_id=$1", x.id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM core.attachments WHERE id=$1 AND state IN('purged','orphaned')", x.id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM core.file_deletions WHERE storage_key=$1", x.key); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// A crash between file fsync and metadata commit can leave an unreferenced blob.
	entries, err := w.scan.next(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !value.ValidUUID(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		var known bool
		if err = w.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.attachments WHERE storage_key=$1 UNION ALL SELECT 1 FROM core.attachment_derivatives WHERE storage_key=$1)", entry.Name()).Scan(&known); err != nil {
			return err
		}
		if !known {
			if err = root.Remove(entry.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
