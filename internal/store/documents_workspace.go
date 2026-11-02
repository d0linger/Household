package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/document"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type DocumentCommand struct {
	Generation        int64            `json:"generation"`
	Version           int64            `json:"version"`
	Action            string           `json:"action"`
	ID                string           `json:"id"`
	Data              json.RawMessage  `json:"data"`
	TargetGenerations map[string]int64 `json:"target_generations,omitempty"`
}

type documentScope struct {
	ctx        context.Context
	tx         pgx.Tx
	cancel     context.CancelFunc
	generation int64
	timezone   string
}

func (q *documentScope) close() { rollback(q.tx); q.cancel() }

func (s *Store) documentRead(ctx context.Context, id auth.Identity) (*documentScope, error) {
	if !energyIdentityValid(id) {
		return nil, ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		cancel()
		return nil, err
	}
	q := &documentScope{ctx: ctx, tx: tx, cancel: cancel}
	var current bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.sessions session JOIN core.users u ON u.id=session.user_id
 JOIN core.memberships member ON member.user_id=u.id WHERE session.id=$1 AND u.id=$2 AND member.household_id=$3
 AND member.id=$4 AND member.role=$5 AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND u.auth_version=$6 AND session.auth_version=u.auth_version AND session.revoked_at IS NULL
 AND session.idle_expires_at>now() AND session.absolute_expires_at>now()`, id.SessionID, id.UserID, id.HouseholdID, id.MembershipID, id.Role, id.AuthVersion).Scan(&current)
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT s.data_generation,h.timezone FROM core.module_states s JOIN core.households h ON h.id=s.household_id WHERE s.household_id=$1 AND s.module_id='documents' AND s.state='enabled'`, id.HouseholdID).Scan(&q.generation, &q.timezone)
	}
	if err != nil {
		q.close()
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return q, nil
}

func documentDecode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 128<<10 || d.Decode(out) != nil {
		return Invalid{"Dokumentvorgang enthält ungültige oder unbekannte Felder"}
	}
	if d.Decode(new(any)) != io.EOF {
		return Invalid{"Mehrfachen Dokumentvorgang prüfen"}
	}
	return nil
}

func (s *Store) DocumentContext(ctx context.Context, id auth.Identity) (Record, error) {
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer q.close()
	views, err := energyRows(q.ctx, q.tx, 100, `SELECT to_jsonb(v) FROM documents.saved_views v WHERE household_id=$1 AND user_id=$2 ORDER BY name,id LIMIT 101`, id.HouseholdID, id.UserID)
	if err != nil {
		return nil, err
	}
	states, err := energyRows(q.ctx, q.tx, 10, `SELECT jsonb_build_object('id',module_id,'state',state,'generation',data_generation) FROM core.module_states WHERE household_id=$1 ORDER BY module_id LIMIT 11`, id.HouseholdID)
	return Record{"generation": q.generation, "timezone": q.timezone, "can_write": id.CanWrite(), "can_configure": id.Role == "admin", "views": views, "module_states": states, "types": document.Types, "deadline_kinds": document.DeadlineKinds}, err
}

func (s *Store) DocumentLibrary(ctx context.Context, id auth.Identity, f document.Filters) (Record, error) {
	if err := f.Validate(); err != nil {
		return nil, Invalid{err.Error()}
	}
	if f.Cursor != "" && !value.ValidUUID(f.Cursor) {
		return nil, Invalid{"Dokumentseite prüfen"}
	}
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer q.close()
	items, err := energyRows(q.ctx, q.tx, 51, `SELECT jsonb_build_object('id',d.id,'title',d.title,'issuer',d.issuer,'category',d.category,'version',d.version,
 'document_type',coalesce(p.document_type,'other'),'review_state',coalesce(p.review_state,'unreviewed'),'archived',d.archived_at IS NOT NULL,
 'file_id',v.id,'attachment_id',v.attachment_id,'snippet',left(coalesce(v.extracted_text,''),240),
 'processing_failed',EXISTS(SELECT 1 FROM documents.processing j WHERE j.document_id=d.id AND j.state='failed'),
 'next_deadline',(SELECT min(due_on) FROM documents.deadlines x WHERE x.document_id=d.id AND x.cancelled_at IS NULL))
 FROM documents.documents d LEFT JOIN documents.profiles p ON p.document_id=d.id LEFT JOIN documents.file_versions v ON v.id=p.current_version_id
 WHERE d.household_id=$1 AND ($2='' OR d.id>nullif($2,'')::uuid) AND (($3='archived')=(d.archived_at IS NOT NULL))
 AND ($4='' OR p.document_type=$4) AND ($3 IN('','archived') OR p.review_state=$3 OR $3='failed' AND EXISTS(SELECT 1 FROM documents.processing j WHERE j.document_id=d.id AND j.state='failed'))
 AND ($5='' OR d.issuer=$5) AND ($6='' OR d.category=$6)
 AND ($7='' OR EXISTS(SELECT 1 FROM documents.deadlines x WHERE x.document_id=d.id AND x.cancelled_at IS NULL AND x.due_on<=nullif($7,'')::date))
 AND ($8='' OR EXISTS(SELECT 1 FROM core.search_documents search WHERE search.household_id=d.household_id AND search.module_id='documents' AND search.module_generation=$9 AND search.resource_type='documents' AND search.resource_id=d.id AND search.search_vector@@websearch_to_tsquery('german',$8)))
 ORDER BY d.id LIMIT 51`, id.HouseholdID, f.Cursor, f.State, f.Type, f.Issuer, f.Category, f.DueBefore, f.Search, q.generation)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = String(items[49]["id"])
	}
	return Record{"items": items, "next_cursor": next, "generation": q.generation}, nil
}

func documentRecord(ctx context.Context, tx pgx.Tx, household, rid string) (Record, error) {
	return energyObject(ctx, tx, `SELECT to_jsonb(d)||jsonb_build_object('document_type',coalesce(p.document_type,'other'),'review_state',coalesce(p.review_state,'unreviewed'),
 'fields',coalesce(p.fields,'{}'),'manual_keys',coalesce(p.manual_keys,'{}'),'current_version_id',p.current_version_id)
 FROM documents.documents d LEFT JOIN documents.profiles p ON p.document_id=d.id WHERE d.household_id=$1 AND d.id=$2`, household, rid)
}

func (s *Store) DocumentDetail(ctx context.Context, id auth.Identity, rid string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer q.close()
	out, err := documentRecord(q.ctx, q.tx, id.HouseholdID, rid)
	if err != nil {
		return nil, err
	}
	for _, list := range []struct{ name, sql string }{
		{"versions", `SELECT jsonb_build_object('id',v.id,'ordinal',v.ordinal,'attachment_id',a.id,'name',a.original_name,'media_type',a.media_type,'size',a.size_bytes,'sha256',encode(v.original_sha256,'hex'),'page_count',v.page_count,'provider',v.text_provider,'source',v.source,'created_at',v.created_at,'preview',EXISTS(SELECT 1 FROM core.attachment_derivatives preview WHERE preview.attachment_id=a.id AND preview.variant='preview')) FROM documents.file_versions v JOIN core.attachments a ON a.id=v.attachment_id AND a.state='ready' AND a.module_generation=$3 WHERE v.household_id=$1 AND v.document_id=$2 ORDER BY v.ordinal DESC LIMIT 101`},
		{"deadlines", `SELECT to_jsonb(x) FROM documents.deadlines x WHERE household_id=$1 AND document_id=$2 AND $3>0 ORDER BY due_on,id LIMIT 101`},
		{"processing", `SELECT jsonb_build_object('id',id,'operation',operation,'version_id',version_id,'state',state,'progress',progress,'status_code',status_code,'created_at',created_at) FROM documents.processing WHERE household_id=$1 AND document_id=$2 AND module_generation=$3 ORDER BY created_at DESC,id LIMIT 101`},
		{"scan_sets", `SELECT to_jsonb(x)||jsonb_build_object('pages',(SELECT jsonb_agg(jsonb_build_object('version_id',p.version_id,'page_number',p.page_number,'position',p.position) ORDER BY position) FROM documents.scan_pages p WHERE p.set_id=x.id)) FROM documents.scan_sets x WHERE household_id=$1 AND document_id=$2 AND $3>0 ORDER BY created_at DESC,id LIMIT 101`},
	} {
		out[list.name], err = energyRows(q.ctx, q.tx, 100, list.sql, id.HouseholdID, rid, q.generation)
		if err != nil {
			return nil, err
		}
	}
	out["links"], err = s.documentCoreLinks(q.ctx, q.tx, id, rid)
	out["generation"] = q.generation
	return out, err
}

func (s *Store) DocumentOverview(ctx context.Context, id auth.Identity, view string) (Record, error) {
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer q.close()
	var rows []Record
	switch view {
	case "deadlines":
		rows, err = energyRows(q.ctx, q.tx, 200, `SELECT to_jsonb(x)||jsonb_build_object('title',d.title,'document_version',d.version) FROM documents.deadlines x JOIN documents.documents d ON d.id=x.document_id WHERE x.household_id=$1 AND x.cancelled_at IS NULL AND d.archived_at IS NULL ORDER BY x.due_on,x.id LIMIT 201`, id.HouseholdID)
	case "rules":
		rows, err = energyRows(q.ctx, q.tx, 100, `SELECT to_jsonb(x) FROM documents.rules x WHERE household_id=$1 ORDER BY name,id LIMIT 101`, id.HouseholdID)
	case "processing":
		rows, err = energyRows(q.ctx, q.tx, 200, `SELECT to_jsonb(x)-'input'||jsonb_build_object('title',d.title) FROM documents.processing x JOIN documents.documents d ON d.id=x.document_id WHERE x.household_id=$1 AND x.module_generation=$2 ORDER BY x.created_at DESC,x.id LIMIT 201`, id.HouseholdID, q.generation)
	case "integrations":
		if id.Role != "admin" {
			return nil, ErrForbidden
		}
		rows, err = energyRows(q.ctx, q.tx, 3, `SELECT to_jsonb(x)-'cursor'-'approved_by' FROM documents.integration_sources x WHERE household_id=$1 ORDER BY adapter LIMIT 4`, id.HouseholdID)
	case "duplicates":
		rows, err = energyRows(q.ctx, q.tx, 100, `SELECT jsonb_build_object('id',d.id,'title',d.title,'other_id',o.id,'other_title',o.title,'reason',CASE WHEN v.original_sha256=w.original_sha256 THEN 'exact_sha256' ELSE 'same_title_issuer' END) FROM documents.documents d JOIN documents.profiles p ON p.document_id=d.id LEFT JOIN documents.file_versions v ON v.id=p.current_version_id JOIN documents.documents o ON o.household_id=d.household_id AND o.id>d.id AND o.archived_at IS NULL JOIN documents.profiles z ON z.document_id=o.id LEFT JOIN documents.file_versions w ON w.id=z.current_version_id WHERE d.household_id=$1 AND d.archived_at IS NULL AND(v.original_sha256=w.original_sha256 OR lower(d.title)=lower(o.title) AND d.issuer<>'' AND lower(d.issuer)=lower(o.issuer)) ORDER BY d.id,o.id LIMIT 101`, id.HouseholdID)
	default:
		return nil, ErrNotFound
	}
	return Record{"items": rows, "generation": q.generation}, err
}

func (s *Store) DocumentWrite(ctx context.Context, id auth.Identity, key, requestID string, in DocumentCommand) (Record, error) {
	if !energyIdentityValid(id) || !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(key) || in.Generation < 1 || in.Version < 0 || in.ID != "" && !value.ValidUUID(in.ID) || len(in.TargetGenerations) > 1 {
		return nil, Invalid{"Dokumentvorgang und Version prüfen"}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "documents", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('household/documents/'||$1::text,0))", id.HouseholdID); err != nil {
		return nil, err
	}
	// Target rights must be checked before receipt replay; saved output contains no private target snapshots.
	if in.Action == "link" {
		if err = s.documentAuthorizeLink(ctx, tx, id, in); err != nil {
			return nil, err
		}
	}
	if in.Action == "integration" && id.Role != "admin" {
		return nil, ErrForbidden
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	var hash, receipt []byte
	var generation int64
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload,module_generation FROM documents.command_receipts WHERE household_id=$1 AND user_id=$2 AND command_key=$3`, id.HouseholdID, id.UserID, key).Scan(&hash, &receipt, &generation)
	if err == nil {
		if !bytes.Equal(hash, digest[:]) || generation != in.Generation {
			return nil, ErrConflict
		}
		return decode(receipt)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	out, err := s.documentApply(ctx, tx, id, in)
	if err != nil {
		return nil, energyError(err)
	}
	receipt, err = json.Marshal(out)
	if err != nil || len(receipt) > 128<<10 {
		return nil, Invalid{"Dokumentantwort zu groß"}
	}
	_, err = tx.Exec(ctx, `INSERT INTO documents.command_receipts(household_id,user_id,command_key,operation,module_generation,request_hash,response_payload) VALUES($1,$2,$3,$4,$5,$6,$7)`, id.HouseholdID, id.UserID, key, in.Action, in.Generation, digest[:], receipt)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return out, err
}
