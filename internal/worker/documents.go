package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/document"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type documentWork struct {
	ID, Household, Document, Version, Operation, RequestedBy string
	Generation                                               int64
	Input                                                    []byte
}

func (w *Worker) documentJob(ctx context.Context, job, lease string) error {
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 48*time.Second)
	defer cancel()
	var work documentWork
	err := w.Pool.QueryRow(ctx, `SELECT p.id,p.household_id,p.document_id,coalesce(p.version_id::text,''),p.operation,p.module_generation,p.requested_by,p.input
 FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='documents'
 JOIN core.memberships member ON member.household_id=p.household_id AND member.user_id=p.requested_by JOIN core.users u ON u.id=member.user_id
 WHERE j.id=$1 AND j.lease_token=$2 AND j.state='running' AND j.lease_until>clock_timestamp() AND p.state IN('pending','running')
 AND m.state='enabled' AND m.data_generation=p.module_generation AND j.module_generation=p.module_generation
 AND member.role IN('admin','editor') AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`, job, lease).
		Scan(&work.ID, &work.Household, &work.Document, &work.Version, &work.Operation, &work.Generation, &work.RequestedBy, &work.Input)
	if errors.Is(err, pgx.ErrNoRows) {
		return w.retireDocumentJob(ctx, job, lease, "authorization_changed")
	}
	if err != nil {
		return err
	}
	_, err = audit.Exec(ctx, w.Pool, `UPDATE documents.processing p SET state='running',progress=10,status_code='processing',updated_at=clock_timestamp()
 WHERE p.id=$1 AND p.state IN('pending','running') AND EXISTS(SELECT 1 FROM core.jobs j WHERE j.id=p.job_id AND j.lease_token=$2 AND j.state='running' AND j.lease_until>clock_timestamp())`, work.ID, lease)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(w.Cfg.UploadDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	done := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				probe, stop := context.WithTimeout(ctx, 2*time.Second)
				var current bool
				e := w.Pool.QueryRow(probe, `SELECT EXISTS(SELECT 1 FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='documents' WHERE p.id=$1 AND p.state='running' AND j.lease_token=$2 AND j.state='running' AND j.lease_until>clock_timestamp() AND m.state='enabled' AND m.data_generation=p.module_generation)`, work.ID, lease).Scan(&current)
				stop()
				if e != nil || !current {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(done); <-joined }()
	result := document.Extracted{}
	var derived []byte
	if work.Operation == "extract" {
		var key, media string
		var expected []byte
		var size int64
		err = w.Pool.QueryRow(ctx, `SELECT a.storage_key,a.media_type,v.original_sha256,v.original_size FROM documents.file_versions v JOIN core.attachments a ON a.id=v.attachment_id AND a.state='ready' AND a.module_generation=$3 WHERE v.household_id=$1 AND v.id=$2`, work.Household, work.Version, work.Generation).Scan(&key, &media, &expected, &size)
		if err == nil {
			err = checkDocumentOriginal(root, key, size, expected)
		}
		if err == nil {
			result, err = document.Extract(ctx, root, key, media, w.Cfg.OCREnabled)
		}
	} else {
		var input struct {
			SetID string `json:"set_id"`
		}
		if json.Unmarshal(work.Input, &input) != nil || !value.ValidUUID(input.SetID) {
			return errors.New("invalid document page operation")
		}
		rows, e := w.Pool.Query(ctx, `SELECT a.storage_key,a.media_type,p.page_number,v.original_size,encode(v.original_sha256,'hex') FROM documents.scan_sets s JOIN documents.scan_pages p ON p.set_id=s.id JOIN documents.file_versions v ON v.id=p.version_id JOIN core.attachments a ON a.id=v.attachment_id AND a.state='ready' AND a.module_generation=$4 WHERE s.household_id=$1 AND s.document_id=$2 AND s.id=$3 ORDER BY p.position LIMIT 21`, work.Household, work.Document, input.SetID, work.Generation)
		if e != nil {
			return e
		}
		pages := []document.ScanSource{}
		proofs := []struct {
			key    string
			size   int64
			digest string
		}{}
		for rows.Next() {
			var p document.ScanSource
			var size int64
			var digest string
			if e = rows.Scan(&p.Key, &p.Media, &p.Page, &size, &digest); e != nil {
				break
			}
			pages = append(pages, p)
			proofs = append(proofs, struct {
				key    string
				size   int64
				digest string
			}{p.Key, size, digest})
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		for _, proof := range proofs {
			digest, decodeErr := hex.DecodeString(proof.digest)
			if decodeErr != nil {
				err = decodeErr
				break
			}
			if err = checkDocumentOriginal(root, proof.key, proof.size, digest); err != nil {
				break
			}
		}
		if err == nil {
			derived, err = document.Assemble(ctx, root, pages)
		}
	}
	if ctx.Err() != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		return w.retireDocumentJob(cleanup, job, lease, "processing_interrupted")
	} // Cancellation/revocation leaves no output or indexed partial text.
	status := "complete"
	if err != nil {
		status = "local_processing_failed"
	}
	return w.finishDocumentJob(ctx, root, job, lease, work, result, derived, status)
}

func checkDocumentOriginal(root *os.Root, key string, size int64, expected []byte) error {
	if !value.ValidUUID(key) || size < 1 || size > 10<<20 || len(expected) != 32 {
		return errors.New("invalid document original proof")
	}
	f, err := root.Open(key)
	if err != nil {
		return errors.New("document original unavailable")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, size+1))
	if err != nil || int64(len(raw)) != size {
		return errors.New("document original size mismatch")
	}
	actual := sha256.Sum256(raw)
	if !bytes.Equal(actual[:], expected) {
		return errors.New("document original checksum mismatch")
	}
	return nil
}

func (w *Worker) finishDocumentJob(ctx context.Context, root *os.Root, job, lease string, work documentWork, result document.Extracted, derived []byte, status string) error {
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, work.Household); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('household/documents/'||$1::text,0))", work.Household); err != nil {
		return err
	}
	var current bool
	err = tx.QueryRow(ctx, `SELECT true FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='documents'
 JOIN core.memberships member ON member.household_id=p.household_id AND member.user_id=p.requested_by JOIN core.users u ON u.id=member.user_id
 WHERE p.id=$1 AND p.state='running' AND j.id=$2 AND j.lease_token=$3 AND j.state='running' AND j.lease_until>clock_timestamp()
 AND m.state='enabled' AND m.data_generation=p.module_generation AND member.role IN('editor','admin') AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password FOR UPDATE OF p,j FOR SHARE OF m,member,u`, work.ID, job, lease).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	written := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, key := range written {
				_ = root.Remove(key)
			}
		}
	}()
	if status == "complete" && work.Operation == "extract" {
		if len(result.Text) > 80000 {
			return errors.New("document extracted text limit exceeded")
		}
		_, err = tx.Exec(ctx, `UPDATE documents.file_versions SET extracted_text=$4,text_provider=$5,page_count=$6 WHERE household_id=$1 AND document_id=$2 AND id=$3`, work.Household, work.Document, work.Version, result.Text, result.Provider, result.Pages)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body)
 SELECT d.household_id,'documents',$3,'documents',d.id,d.title,left(d.issuer||' '||d.category||' '||d.notes||' '||v.extracted_text,90000)
 FROM documents.documents d JOIN documents.profiles p ON p.document_id=d.id JOIN documents.file_versions v ON v.id=p.current_version_id WHERE d.household_id=$1 AND d.id=$2 AND d.archived_at IS NULL
 ON CONFLICT(household_id,module_id,resource_type,resource_id) DO UPDATE SET title=excluded.title,body=excluded.body,module_generation=excluded.module_generation,updated_at=clock_timestamp()`, work.Household, work.Document, work.Generation)
		}
		if err == nil && len(result.Preview) > 0 {
			var attachment string
			err = tx.QueryRow(ctx, `SELECT attachment_id FROM documents.file_versions WHERE id=$1`, work.Version).Scan(&attachment)
			if err == nil {
				key, e := writeDocumentResult(root, result.Preview)
				err = e
				if e == nil {
					written = append(written, key)
					sum := sha256.Sum256(result.Preview)
					_, err = tx.Exec(ctx, `INSERT INTO core.attachment_derivatives(attachment_id,variant,storage_key,media_type,size_bytes,sha256) VALUES($1,'preview',$2,'image/png',$3,$4) ON CONFLICT(attachment_id,variant) DO NOTHING`, attachment, key, len(result.Preview), sum[:])
					if err == nil {
						var stored string
						err = tx.QueryRow(ctx, `SELECT storage_key FROM core.attachment_derivatives WHERE attachment_id=$1 AND variant='preview'`, attachment).Scan(&stored)
						if err == nil && stored != key {
							_ = root.Remove(key)
						}
					}
				}
			}
		}
	}
	if status == "complete" && work.Operation == "scan" {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "upload-quota/"+work.Household); err != nil {
			return err
		}
		var used int64
		if err = tx.QueryRow(ctx, `SELECT coalesce(sum(size_bytes),0) FROM core.attachments WHERE household_id=$1 AND state='ready'`, work.Household).Scan(&used); err != nil {
			return err
		}
		if used+int64(len(derived)) > w.Cfg.UploadQuota {
			return errors.New("document quota reached")
		}
		key, e := writeDocumentResult(root, derived)
		err = e
		if e == nil {
			written = append(written, key)
			aid := value.UUID()
			sum := sha256.Sum256(derived)
			_, err = tx.Exec(ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,'documents',$3,'documents',$4,$5,'Scan.pdf','application/pdf',$6,$7,$8)`, aid, work.Household, work.Generation, work.Document, key, len(derived), sum[:], work.RequestedBy)
			if err == nil {
				_, err = store.RegisterDocumentOriginal(ctx, tx, auth.Identity{HouseholdID: work.Household, UserID: work.RequestedBy}, work.Document, aid, "derived", work.Generation, false)
			}
		}
	}
	if err != nil {
		return err
	}
	state, jobState := "done", "done"
	if status != "complete" {
		state, jobState = "failed", "failed"
	}
	_, err = tx.Exec(ctx, `UPDATE documents.processing SET state=$2,progress=CASE WHEN $2='done' THEN 100 ELSE progress END,status_code=$3,updated_at=clock_timestamp() WHERE id=$1`, work.ID, state, status)
	if err == nil {
		tag, e := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,last_error=CASE WHEN $3='failed' THEN 'local document processing failed' END,finished_at=clock_timestamp(),lease_until=NULL,lease_token=NULL WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp()`, job, lease, jobState)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			return nil
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	committed = err == nil
	return err
}

func (w *Worker) retireDocumentJob(ctx context.Context, job, lease, code string) error {
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE documents.processing p SET state='failed',status_code=$3,updated_at=clock_timestamp() FROM core.jobs j WHERE p.job_id=j.id AND j.id=$1 AND j.kind='document_processing' AND j.lease_token=$2 AND j.state='running' AND p.state IN('pending','running')`, job, lease, code)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE core.jobs SET state='failed',last_error='document job no longer authorized or interrupted',finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND kind IN('document_processing','document_input') AND state='running'`, job, lease)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeDocumentResult(root *os.Root, data []byte) (string, error) {
	if len(data) == 0 || len(data) > 10<<20 {
		return "", fmt.Errorf("document result size invalid")
	}
	key := value.UUID()
	f, err := root.OpenFile(key, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = root.Remove(key)
		return "", err
	}
	return key, nil
}
