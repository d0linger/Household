package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func PrepareDocumentUpload(ctx context.Context, tx pgx.Tx, id auth.Identity, rid, key string, version, generation int64, digest []byte) (Record, bool, error) {
	if !value.ValidUUID(key) || len(digest) != 32 {
		return nil, false, Invalid{"Dateivorgang benötigt einen eindeutigen Wiederholungsschlüssel"}
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('household/documents/'||$1::text,0))", id.HouseholdID); err != nil {
		return nil, false, err
	}
	var stored, raw []byte
	var oldGen int64
	err := tx.QueryRow(ctx, `SELECT request_hash,response_payload,module_generation FROM documents.command_receipts WHERE household_id=$1 AND user_id=$2 AND command_key=$3 AND operation='upload'`, id.HouseholdID, id.UserID, key).Scan(&stored, &raw, &oldGen)
	if err == nil {
		if !bytes.Equal(stored, digest) || oldGen != generation {
			return nil, false, ErrConflict
		}
		out, e := decode(raw)
		return out, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	if err = documentLock(ctx, tx, id, rid, version); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func RegisterDocumentOriginal(ctx context.Context, tx pgx.Tx, id auth.Identity, rid, attachment, source string, generation int64, current bool) (Record, error) {
	if !value.ValidUUID(rid) || !value.ValidUUID(attachment) {
		return nil, Invalid{"Dokumentquelle prüfen"}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO documents.profiles(document_id,household_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, rid, id.HouseholdID); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM documents.file_versions WHERE household_id=$1 AND document_id=$2`, id.HouseholdID, rid).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 100 {
		return nil, Invalid{"Höchstens 100 unveränderte Dateiversionen pro Dokument"}
	}
	vid := value.UUID()
	tag, err := tx.Exec(ctx, `INSERT INTO documents.file_versions(id,household_id,document_id,attachment_id,ordinal,source,original_sha256,original_size,created_by)
 SELECT $1,$2,$3,a.id,coalesce((SELECT max(ordinal)+1 FROM documents.file_versions WHERE document_id=$3),1),$5,a.sha256,a.size_bytes,$6 FROM core.attachments a WHERE a.id=$4 AND a.household_id=$2 AND a.module_id='documents' AND a.resource_type='documents' AND a.resource_id=$3 AND a.module_generation=$7 AND a.state='ready'`, vid, id.HouseholdID, rid, attachment, source, id.UserID, generation)
	if err == nil && tag.RowsAffected() != 1 {
		err = ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if current {
		_, err = tx.Exec(ctx, `UPDATE documents.profiles SET current_version_id=$3,review_state='unreviewed',updated_at=clock_timestamp() WHERE household_id=$1 AND document_id=$2`, id.HouseholdID, rid, vid)
	}
	if err == nil {
		err = documentTouch(ctx, tx, id, rid)
	}
	if err == nil {
		err = documentIndex(ctx, tx, id, rid, generation)
	}
	return Record{"id": rid, "attachment_id": attachment, "version_id": vid, "current": current}, err
}

func SaveDocumentUploadReceipt(ctx context.Context, tx pgx.Tx, id auth.Identity, key string, generation int64, digest []byte, out Record) error {
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO documents.command_receipts(household_id,user_id,command_key,operation,module_generation,request_hash,response_payload) VALUES($1,$2,$3,'upload',$4,$5,$6)`, id.HouseholdID, id.UserID, key, generation, digest, raw)
	return err
}
