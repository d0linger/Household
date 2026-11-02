package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/documentinput"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (w *Worker) scheduleDocumentInputs(ctx context.Context) error {
	_, err := audit.Exec(ctx, w.Pool, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key)
 SELECT s.household_id,'documents',s.module_generation,'document_input',jsonb_build_object('source_id',s.id,'source_version',s.version),
 'document-input/'||s.id::text||'/'||s.version::text||'/'||to_char(clock_timestamp(),'YYYY-MM-DD-HH24-MI')
 FROM documents.integration_sources s JOIN core.module_states m ON m.household_id=s.household_id AND m.module_id='documents'
 JOIN core.memberships member ON member.household_id=s.household_id AND member.user_id=s.approved_by JOIN core.users u ON u.id=member.user_id
 WHERE s.enabled AND m.state='enabled' AND m.data_generation=s.module_generation AND s.next_poll_at<=clock_timestamp()
 AND member.role='admin' AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND(s.adapter='watch' AND $1 OR s.adapter='imap' AND $2 OR s.adapter='paperless' AND $3)
 AND NOT EXISTS(SELECT 1 FROM core.jobs j WHERE j.household_id=s.household_id AND j.kind='document_input' AND j.state IN('pending','running') AND j.payload->>'source_id'=s.id::text)
 ORDER BY s.household_id,s.id LIMIT 3 ON CONFLICT(dedupe_key) DO NOTHING`, w.Cfg.DocumentInputs.Available("watch"), w.Cfg.DocumentInputs.Available("imap"), w.Cfg.DocumentInputs.Available("paperless"))
	return err
}

func (w *Worker) documentInput(ctx context.Context, job, lease string) (jobErr error) {
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 48*time.Second)
	defer cancel()
	var source, household, adapter, actor string
	var generation, version int64
	var raw []byte
	err := w.Pool.QueryRow(ctx, `SELECT s.id,s.household_id,s.adapter,s.approved_by,s.module_generation,s.version,s.cursor FROM documents.integration_sources s
 JOIN core.jobs j ON j.payload->>'source_id'=s.id::text AND (j.payload->>'source_version')::bigint=s.version
 JOIN core.module_states m ON m.household_id=s.household_id AND m.module_id='documents'
 JOIN core.memberships member ON member.household_id=s.household_id AND member.user_id=s.approved_by JOIN core.users u ON u.id=member.user_id
 WHERE j.id=$1 AND j.lease_token=$2 AND j.state='running' AND j.lease_until>clock_timestamp() AND s.enabled
 AND m.state='enabled' AND m.data_generation=s.module_generation AND j.module_generation=s.module_generation
 AND member.role='admin' AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`, job, lease).Scan(&source, &household, &adapter, &actor, &generation, &version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return w.retireDocumentJob(ctx, job, lease, "authorization_changed")
	}
	if err != nil {
		return err
	}
	var cursor documentinput.Cursor
	if json.Unmarshal(raw, &cursor) != nil {
		return errors.New("invalid document source cursor")
	}
	defer func() {
		if jobErr != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer stop()
			_, _ = audit.Exec(cleanup, w.Pool, `UPDATE documents.integration_sources SET status_code='input_failed',failures=least(failures+1,1000),next_poll_at=clock_timestamp()+interval '60 seconds' WHERE id=$1 AND version=$2 AND enabled`, source, version)
		}
	}()
	var candidates []documentinput.Candidate
	next := cursor
	if !w.Cfg.DocumentInputs.Available(adapter) {
		return nil
	}
	switch adapter {
	case "watch":
		candidates, next, err = documentinput.Watch(ctx, w.Cfg.DocumentInputs.WatchDir, cursor)
	case "imap":
		candidates, next, err = documentinput.IMAP(ctx, w.Cfg.DocumentInputs, cursor)
	case "paperless":
		candidates, next, err = documentinput.Paperless(ctx, w.Cfg.DocumentInputs, cursor)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	tx, e := audit.Begin(ctx, w.Pool)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, e = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('household/documents/'||$1::text,0))", household); e != nil {
		return e
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT true FROM documents.integration_sources s JOIN core.jobs j ON j.payload->>'source_id'=s.id::text
 JOIN core.module_states m ON m.household_id=s.household_id AND m.module_id='documents'
 JOIN core.memberships member ON member.household_id=s.household_id AND member.user_id=s.approved_by JOIN core.users u ON u.id=member.user_id
 WHERE s.id=$1 AND s.version=$2 AND s.enabled AND j.id=$3 AND j.lease_token=$4 AND j.state='running' AND j.lease_until>clock_timestamp()
 AND m.state='enabled' AND m.data_generation=s.module_generation AND member.role='admin' AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 FOR UPDATE OF s,j FOR SHARE OF m,member,u`, source, version, job, lease).Scan(&valid)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if err != nil {
		_, e = tx.Exec(ctx, `UPDATE documents.integration_sources SET status_code='input_failed',failures=least(failures+1,1000),next_poll_at=clock_timestamp()+make_interval(secs=>least(3600,60*(failures+1))) WHERE id=$1`, source)
	} else {
		if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "upload-quota/"+household); e != nil {
			return e
		}
		root, openErr := os.OpenRoot(w.Cfg.UploadDir)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = root.Close() }()
		written := []string{}
		committed := false
		defer func() {
			if !committed {
				for _, key := range written {
					_ = root.Remove(key)
				}
			}
		}()
		for _, candidate := range candidates {
			media, inputErr := documentInputMedia(candidate.Bytes)
			if inputErr != nil {
				return inputErr
			}
			if len(candidate.Key) == 0 || len(candidate.Key) > 300 {
				return errors.New("document input identity exceeds limit")
			}
			sum := sha256.Sum256(candidate.Bytes)
			var duplicate bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents.import_receipts WHERE source_id=$1 AND remote_key=$2 AND content_sha256=$3)`, source, candidate.Key, sum[:]).Scan(&duplicate); e != nil {
				return e
			}
			if duplicate {
				continue
			}
			var used int64
			if e = tx.QueryRow(ctx, `SELECT coalesce(sum(size_bytes),0) FROM core.attachments WHERE household_id=$1 AND state='ready'`, household).Scan(&used); e != nil {
				return e
			}
			if used+int64(len(candidate.Bytes)) > w.Cfg.UploadQuota {
				return errors.New("document input storage quota reached")
			}
			key, writeErr := writeDocumentResult(root, candidate.Bytes)
			if writeErr != nil {
				return writeErr
			}
			written = append(written, key)
			rid, aid := value.UUID(), value.UUID()
			name := filepath.Base(strings.ReplaceAll(candidate.Name, "\\", "/"))
			if !utf8.ValidString(name) {
				return errors.New("invalid document input filename")
			}
			runes := []rune(name)
			if len(runes) > 180 {
				name = string(runes[:180])
			}
			if name == "" || name == "." {
				name = "Neues Dokument"
			}
			_, e = tx.Exec(ctx, `INSERT INTO documents.documents(id,household_id,title,created_by) VALUES($1,$2,$3,$4)`, rid, household, name, actor)
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,'documents',$3,'documents',$4,$5,$6,$7,$8,$9,$10)`, aid, household, generation, rid, key, name, media, len(candidate.Bytes), sum[:], actor)
			if e != nil {
				return e
			}
			_, e = store.RegisterDocumentOriginal(ctx, tx, auth.Identity{HouseholdID: household, UserID: actor}, rid, aid, adapter, generation, true)
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `INSERT INTO documents.import_receipts(household_id,source_id,remote_key,content_sha256,document_id) VALUES($1,$2,$3,$4,$5)`, household, source, candidate.Key, sum[:], rid)
			if e != nil {
				return e
			}
			if media != "application/pdf" {
				payload, _ := json.Marshal(store.Record{"attachment_id": aid, "storage_key": key})
				_, e = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key) VALUES($1,'documents',$2,'thumbnail',$3,$4)`, household, generation, payload, "thumbnail/"+aid)
				if e != nil {
					return e
				}
			}
		}
		cursorBytes, _ := json.Marshal(next)
		_, e = tx.Exec(ctx, `UPDATE documents.integration_sources SET cursor=$2,status_code='ready',failures=0,next_poll_at=clock_timestamp()+interval '60 seconds' WHERE id=$1`, source, cursorBytes)
		if e != nil {
			return e
		}
		tag, finishErr := tx.Exec(ctx, `UPDATE core.jobs SET state='done',finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp()`, job, lease)
		if finishErr != nil {
			return finishErr
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		e = tx.Commit(ctx)
		committed = e == nil
		return e
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE core.jobs SET state='failed',last_error='document input failed',finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp()`, job, lease)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func documentInputMedia(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 10<<20 {
		return "", errors.New("document input file limit reached")
	}
	if bytes.HasPrefix(raw, []byte("%PDF-")) {
		return "application/pdf", nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20000000 || format != "jpeg" && format != "png" {
		return "", errors.New("document input must be a bounded PDF, JPEG or PNG")
	}
	if _, _, err = image.Decode(bytes.NewReader(raw)); err != nil {
		return "", errors.New("document input image is corrupt")
	}
	return "image/" + format, nil
}
