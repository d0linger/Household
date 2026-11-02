package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/document"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func documentLock(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, version int64) error {
	if !value.ValidUUID(rid) || version < 1 {
		return Invalid{"Dokument und Versionsstand erneut wählen"}
	}
	var actual int64
	err := tx.QueryRow(ctx, `SELECT version FROM documents.documents WHERE household_id=$1 AND id=$2 FOR UPDATE`, id.HouseholdID, rid).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if actual != version {
		return ErrConflict
	}
	return nil
}

func documentTouch(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string) error {
	_, err := tx.Exec(ctx, `UPDATE documents.documents SET version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, rid)
	return err
}

func documentIndex(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, generation int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body)
 SELECT d.household_id,'documents',$3,'documents',d.id,d.title,left(d.issuer||' '||d.category||' '||d.notes||' '||coalesce(v.extracted_text,''),90000)
 FROM documents.documents d LEFT JOIN documents.profiles p ON p.document_id=d.id LEFT JOIN documents.file_versions v ON v.id=p.current_version_id
 WHERE d.household_id=$1 AND d.id=$2 AND d.archived_at IS NULL
 ON CONFLICT(household_id,module_id,resource_type,resource_id) DO UPDATE SET title=excluded.title,body=excluded.body,module_generation=excluded.module_generation,updated_at=clock_timestamp()`, id.HouseholdID, rid, generation)
	return err
}

func (s *Store) documentSave(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand, m document.Metadata) (Record, error) {
	if err := m.Validate(); err != nil {
		return nil, Invalid{err.Error()}
	}
	rid := in.ID
	if rid == "" {
		if in.Version != 0 {
			return nil, ErrConflict
		}
		rid = value.UUID()
		if _, err := tx.Exec(ctx, `INSERT INTO documents.documents(id,household_id,title,category,issuer,notes,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, rid, id.HouseholdID, strings.TrimSpace(m.Title), m.Category, m.Issuer, m.Notes, id.UserID); err != nil {
			return nil, err
		}
	} else {
		if err := documentLock(ctx, tx, id, rid, in.Version); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE documents.documents SET title=$3,category=$4,issuer=$5,notes=$6,version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, rid, strings.TrimSpace(m.Title), m.Category, m.Issuer, m.Notes); err != nil {
			return nil, err
		}
	}
	fields, err := json.Marshal(m.Fields)
	if err != nil {
		return nil, err
	}
	if m.Fields == nil {
		fields = []byte("{}")
	}
	state := "unreviewed"
	if m.Reviewed {
		state = "reviewed"
	}
	_, err = tx.Exec(ctx, `INSERT INTO documents.profiles(document_id,household_id,document_type,review_state,fields,manual_keys,reviewed_at)
 VALUES($1,$2,$3,$4,$5,ARRAY['title','category','issuer','notes','document_type','fields'],CASE WHEN $4='reviewed' THEN clock_timestamp() END)
 ON CONFLICT(document_id) DO UPDATE SET document_type=excluded.document_type,review_state=excluded.review_state,fields=excluded.fields,
 manual_keys=excluded.manual_keys,reviewed_at=excluded.reviewed_at,updated_at=clock_timestamp()`, rid, id.HouseholdID, m.Type, state, fields)
	if err == nil {
		err = documentIndex(ctx, tx, id, rid, in.Generation)
	}
	if err != nil {
		return nil, err
	}
	return documentRecord(ctx, tx, id.HouseholdID, rid)
}

func (s *Store) documentApply(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	switch in.Action {
	case "view", "rule_save":
		var count int
		table := "documents.saved_views"
		if in.Action == "rule_save" {
			table = "documents.rules"
		}
		// security: table is the closed local action allowlist, never a request identifier.
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE household_id=$1", id.HouseholdID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 100 && in.ID == "" {
			return nil, Invalid{"Höchstens 100 gespeicherte Ansichten beziehungsweise Regeln pro Haushalt"}
		}
	}
	switch in.Action {
	case "save":
		var m document.Metadata
		if err := documentDecode(in.Data, &m); err != nil {
			return nil, err
		}
		return s.documentSave(ctx, tx, id, in, m)
	case "view":
		var v struct {
			Name    string           `json:"name"`
			Filters document.Filters `json:"filters"`
		}
		if err := documentDecode(in.Data, &v); err != nil {
			return nil, err
		}
		if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 {
			return nil, Invalid{"Ansichtsname prüfen"}
		}
		if err := v.Filters.Validate(); err != nil {
			return nil, Invalid{err.Error()}
		}
		if v.Filters.Cursor != "" {
			return nil, Invalid{"Ansicht ohne Seitencursor speichern"}
		}
		raw, _ := json.Marshal(v.Filters)
		var rid string
		err := tx.QueryRow(ctx, `INSERT INTO documents.saved_views(household_id,user_id,name,filters) VALUES($1,$2,$3,$4) ON CONFLICT(household_id,user_id,name) DO UPDATE SET filters=excluded.filters,version=documents.saved_views.version+1 RETURNING id`, id.HouseholdID, id.UserID, v.Name, raw).Scan(&rid)
		return Record{"id": rid}, err
	case "view_delete":
		tag, err := tx.Exec(ctx, `DELETE FROM documents.saved_views WHERE household_id=$1 AND user_id=$2 AND id=$3 AND version=$4`, id.HouseholdID, id.UserID, in.ID, in.Version)
		if err == nil && tag.RowsAffected() != 1 {
			err = ErrConflict
		}
		return Record{"id": in.ID}, err
	case "batch_preview", "batch_apply":
		return s.documentBatch(ctx, tx, id, in)
	case "rule_save":
		return s.documentRuleSave(ctx, tx, id, in)
	case "rule_preview", "rule_apply":
		return s.documentRuleApply(ctx, tx, id, in)
	case "integration":
		return s.documentIntegration(ctx, tx, id, in)
	}
	if err := documentLock(ctx, tx, id, in.ID, in.Version); err != nil {
		return nil, err
	}
	out := Record{"id": in.ID}
	var err error
	switch in.Action {
	case "archive":
		var v struct {
			Archived bool `json:"archived"`
		}
		if err = documentDecode(in.Data, &v); err == nil {
			_, err = tx.Exec(ctx, `UPDATE documents.documents SET archived_at=CASE WHEN $3 THEN clock_timestamp() END WHERE household_id=$1 AND id=$2`, id.HouseholdID, in.ID, v.Archived)
		}
		if err == nil && v.Archived {
			_, err = tx.Exec(ctx, `DELETE FROM core.search_documents WHERE household_id=$1 AND module_id='documents' AND resource_id=$2`, id.HouseholdID, in.ID)
		}
		if err == nil && v.Archived {
			_, err = tx.Exec(ctx, `UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp() WHERE household_id=$1 AND module_id='documents' AND resource_id=$2 AND state='pending'`, id.HouseholdID, in.ID)
		}
		if err == nil && !v.Archived {
			_, err = tx.Exec(ctx, `INSERT INTO core.reminders(household_id,module_id,module_generation,resource_type,resource_id,kind,scheduled_at,timezone,dedupe_key)
 SELECT x.household_id,'documents',$3,'documents',x.document_id,'document_expiry',(x.due_on::timestamp+interval '9 hours'-make_interval(days=>x.remind_days)) AT TIME ZONE h.timezone,h.timezone,'document-deadline/'||x.id::text||'/reactivated/'||$4
 FROM documents.deadlines x JOIN core.households h ON h.id=x.household_id WHERE x.household_id=$1 AND x.document_id=$2 AND x.cancelled_at IS NULL`, id.HouseholdID, in.ID, in.Generation, value.UUID())
		}
	case "current":
		var v struct {
			VersionID string `json:"version_id"`
		}
		if err = documentDecode(in.Data, &v); err == nil {
			if !value.ValidUUID(v.VersionID) {
				return nil, Invalid{"Dateiversion prüfen"}
			}
			tag, e := tx.Exec(ctx, `UPDATE documents.profiles p SET current_version_id=v.id,updated_at=clock_timestamp() FROM documents.file_versions v WHERE p.household_id=$1 AND p.document_id=$2 AND v.household_id=p.household_id AND v.document_id=p.document_id AND v.id=$3`, id.HouseholdID, in.ID, v.VersionID)
			err = e
			if err == nil && tag.RowsAffected() != 1 {
				err = ErrNotFound
			}
		}
	case "deadline":
		out, err = s.documentDeadline(ctx, tx, id, in)
	case "link":
		out, err = s.documentCoreSaveLink(ctx, tx, id, in)
	case "scan_preview", "scan_save", "scan_process":
		out, err = s.documentScan(ctx, tx, id, in)
	case "extract":
		var v struct {
			VersionID string `json:"version_id"`
		}
		if err = documentDecode(in.Data, &v); err == nil {
			out, err = documentQueue(ctx, tx, id, in, v.VersionID, "extract", []byte("{}"))
		}
	case "cancel", "retry":
		out, err = documentControlJob(ctx, tx, id, in)
	default:
		return nil, Invalid{"Unbekannter Dokumentvorgang"}
	}
	if err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"scan_preview"}, in.Action) {
		if err = documentTouch(ctx, tx, id, in.ID); err != nil {
			return nil, err
		}
		if err = documentIndex(ctx, tx, id, in.ID, in.Generation); err != nil {
			return nil, err
		}
	}
	current, err := documentRecord(ctx, tx, id.HouseholdID, in.ID)
	if err == nil {
		out["document"] = current
	}
	return out, err
}

type documentBatchItem struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type documentBatchInput struct {
	Items    []documentBatchItem `json:"items"`
	Issuer   *string             `json:"issuer"`
	Category *string             `json:"category"`
}

func (s *Store) documentBatch(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v documentBatchInput
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if len(v.Items) < 1 || len(v.Items) > 50 || v.Issuer == nil && v.Category == nil || v.Issuer != nil && len(*v.Issuer) > 200 || v.Category != nil && len(*v.Category) > 200 {
		return nil, Invalid{"1 bis 50 Dokumente und begrenzte Änderungen auswählen"}
	}
	slices.SortFunc(v.Items, func(a, b documentBatchItem) int { return strings.Compare(a.ID, b.ID) })
	out := []Record{}
	last := ""
	for _, item := range v.Items {
		if item.ID == last {
			return nil, Invalid{"Dokument nur einmal auswählen"}
		}
		last = item.ID
		if err := documentLock(ctx, tx, id, item.ID, item.Version); err != nil {
			return nil, err
		}
		old, err := documentRecord(ctx, tx, id.HouseholdID, item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Record{"id": item.ID, "title": old["title"], "version": item.Version, "before": Record{"issuer": old["issuer"], "category": old["category"]}, "after": Record{"issuer": v.Issuer, "category": v.Category}})
		if in.Action == "batch_apply" {
			_, err = tx.Exec(ctx, `UPDATE documents.documents SET issuer=coalesce($3,issuer),category=coalesce($4,category),version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, item.ID, v.Issuer, v.Category)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO documents.profiles(document_id,household_id,manual_keys) VALUES($1,$2,ARRAY['issuer','category']) ON CONFLICT(document_id) DO UPDATE SET manual_keys=ARRAY(SELECT DISTINCT unnest(documents.profiles.manual_keys||ARRAY['issuer','category']))`, item.ID, id.HouseholdID)
			}
			if err == nil {
				err = documentIndex(ctx, tx, id, item.ID, in.Generation)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return Record{"items": out, "applied": in.Action == "batch_apply"}, nil
}

func (s *Store) documentDeadline(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v struct {
		ID         string `json:"deadline_id"`
		Version    int64  `json:"deadline_version"`
		Kind       string `json:"kind"`
		DueOn      string `json:"due_on"`
		Label      string `json:"label"`
		RemindDays int    `json:"remind_days"`
		Source     string `json:"source"`
		Confirmed  bool   `json:"confirmed"`
		Cancelled  bool   `json:"cancelled"`
	}
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if !v.Confirmed || !slices.Contains(document.DeadlineKinds, v.Kind) || strings.TrimSpace(v.Label) == "" || len(v.Label) > 200 || strings.TrimSpace(v.Source) == "" || len(v.Source) > 200 || v.RemindDays < 0 || v.RemindDays > 366 {
		return nil, Invalid{"Fristart, Quelle und Datum ausdrücklich bestätigen"}
	}
	if _, err := time.Parse(time.DateOnly, v.DueOn); err != nil {
		return nil, Invalid{"Fristdatum prüfen"}
	}
	rid := v.ID
	var err error
	if rid == "" {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM documents.deadlines WHERE household_id=$1 AND document_id=$2`, id.HouseholdID, in.ID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 100 {
			return nil, Invalid{"Höchstens 100 bestätigte Fristen je Dokument"}
		}
	}
	if rid == "" {
		rid = value.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO documents.deadlines(id,household_id,document_id,kind,due_on,label,remind_days,source,confirmed_by,cancelled_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,CASE WHEN $10 THEN clock_timestamp() END)`, rid, id.HouseholdID, in.ID, v.Kind, v.DueOn, v.Label, v.RemindDays, v.Source, id.UserID, v.Cancelled)
	} else {
		if !value.ValidUUID(rid) || v.Version < 1 {
			return nil, Invalid{"Fristversion prüfen"}
		}
		tag, e := tx.Exec(ctx, `UPDATE documents.deadlines SET kind=$5,due_on=$6,label=$7,remind_days=$8,source=$9,confirmed_by=$10,cancelled_at=CASE WHEN $11 THEN clock_timestamp() END,version=version+1 WHERE household_id=$1 AND document_id=$2 AND id=$3 AND version=$4`, id.HouseholdID, in.ID, rid, v.Version, v.Kind, v.DueOn, v.Label, v.RemindDays, v.Source, id.UserID, v.Cancelled)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			err = ErrConflict
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp() WHERE household_id=$1 AND module_id='documents' AND dedupe_key LIKE $2 AND state='pending'`, id.HouseholdID, "document-deadline/"+rid+"/%"); err != nil {
		return nil, err
	}
	if !v.Cancelled {
		_, err = tx.Exec(ctx, `INSERT INTO core.reminders(household_id,module_id,module_generation,resource_type,resource_id,kind,scheduled_at,timezone,dedupe_key)
 SELECT id,'documents',$2,'documents',$3,'document_expiry',($4::date::timestamp+interval '9 hours'-make_interval(days=>$5)) AT TIME ZONE timezone,timezone,$6
 FROM core.households WHERE id=$1 ON CONFLICT(dedupe_key) DO UPDATE SET state='pending',cancelled_at=NULL,scheduled_at=excluded.scheduled_at`, id.HouseholdID, in.Generation, in.ID, v.DueOn, v.RemindDays, "document-deadline/"+rid+"/"+v.DueOn+"/"+value.UUID())
	}
	return Record{"id": in.ID, "deadline_id": rid}, err
}

func (s *Store) documentRuleSave(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v document.Rule
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if err := v.Validate(); err != nil {
		return nil, Invalid{err.Error()}
	}
	rid := in.ID
	var err error
	if rid == "" {
		rid = value.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO documents.rules(id,household_id,name,contains_text,document_type,issuer,category,enabled,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, rid, id.HouseholdID, v.Name, v.Contains, v.Type, v.Issuer, v.Category, v.Enabled, id.UserID)
	} else {
		tag, e := tx.Exec(ctx, `UPDATE documents.rules SET name=$4,contains_text=$5,document_type=$6,issuer=$7,category=$8,enabled=$9,version=version+1 WHERE household_id=$1 AND id=$2 AND version=$3`, id.HouseholdID, rid, in.Version, v.Name, v.Contains, v.Type, v.Issuer, v.Category, v.Enabled)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			err = ErrConflict
		}
	}
	return Record{"id": rid}, err
}

func (s *Store) documentRuleApply(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v struct {
		RuleID      string              `json:"rule_id"`
		RuleVersion int64               `json:"rule_version"`
		Items       []documentBatchItem `json:"items"`
	}
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if !value.ValidUUID(v.RuleID) || v.RuleVersion < 1 || len(v.Items) < 1 || len(v.Items) > 50 {
		return nil, Invalid{"Regel und 1 bis 50 Dokumente auswählen"}
	}
	var rule document.Rule
	err := tx.QueryRow(ctx, `SELECT name,contains_text,document_type,issuer,category,enabled FROM documents.rules WHERE household_id=$1 AND id=$2 AND version=$3 FOR SHARE`, id.HouseholdID, v.RuleID, v.RuleVersion).Scan(&rule.Name, &rule.Contains, &rule.Type, &rule.Issuer, &rule.Category, &rule.Enabled)
	if err != nil {
		return nil, energyError(err)
	}
	slices.SortFunc(v.Items, func(a, b documentBatchItem) int { return strings.Compare(a.ID, b.ID) })
	out := []Record{}
	last := ""
	for _, item := range v.Items {
		if last == item.ID {
			return nil, Invalid{"Doppeltes Dokument"}
		}
		last = item.ID
		if err = documentLock(ctx, tx, id, item.ID, item.Version); err != nil {
			return nil, err
		}
		var source string
		var manual []string
		err = tx.QueryRow(ctx, `SELECT d.title||' '||d.issuer||' '||coalesce(v.extracted_text,''),coalesce(p.manual_keys,'{}') FROM documents.documents d LEFT JOIN documents.profiles p ON p.document_id=d.id LEFT JOIN documents.file_versions v ON v.id=p.current_version_id WHERE d.household_id=$1 AND d.id=$2`, id.HouseholdID, item.ID).Scan(&source, &manual)
		if err != nil {
			return nil, err
		}
		changes := document.RuleChanges(rule, source, manual)
		out = append(out, Record{"id": item.ID, "version": item.Version, "changes": changes, "reason": "Text enthält: " + rule.Contains, "protected_fields": manual})
		if in.Action == "rule_apply" && len(changes) > 0 {
			_, err = tx.Exec(ctx, `UPDATE documents.documents SET issuer=coalesce(nullif($3,''),issuer),category=coalesce(nullif($4,''),category),version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, item.ID, changes["issuer"], changes["category"])
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE documents.profiles SET document_type=coalesce(nullif($3,''),document_type),updated_at=clock_timestamp() WHERE household_id=$1 AND document_id=$2`, id.HouseholdID, item.ID, changes["document_type"])
			}
			if err == nil {
				err = documentIndex(ctx, tx, id, item.ID, in.Generation)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return Record{"items": out, "applied": in.Action == "rule_apply"}, nil
}

func (s *Store) documentScan(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v struct {
		Label string          `json:"label"`
		Pages []document.Page `json:"pages"`
	}
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if err := document.ValidatePages(v.Pages); err != nil {
		return nil, Invalid{err.Error()}
	}
	if strings.TrimSpace(v.Label) == "" || len(v.Label) > 100 {
		return nil, Invalid{"Scanbezeichnung prüfen"}
	}
	for _, p := range v.Pages {
		if !value.ValidUUID(p.VersionID) {
			return nil, Invalid{"Seitendatei prüfen"}
		}
		var found bool
		if err := tx.QueryRow(ctx, `SELECT true FROM documents.file_versions v JOIN documents.documents d ON d.id=v.document_id JOIN core.attachments a ON a.id=v.attachment_id AND a.state='ready' AND a.module_generation=$4 WHERE v.household_id=$1 AND v.id=$2 AND v.page_count>=$3 AND d.archived_at IS NULL FOR SHARE OF v,d,a`, id.HouseholdID, p.VersionID, p.PageNumber, in.Generation).Scan(&found); err != nil {
			return nil, energyError(err)
		}
	}
	out := Record{"id": in.ID, "pages": v.Pages, "label": v.Label, "originals_preserved": true}
	if in.Action == "scan_preview" {
		return out, nil
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM documents.scan_sets WHERE household_id=$1 AND document_id=$2`, id.HouseholdID, in.ID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 100 {
		return nil, Invalid{"Höchstens 100 Seitensätze je Dokument"}
	}
	sid := value.UUID()
	_, err := tx.Exec(ctx, `INSERT INTO documents.scan_sets(id,household_id,document_id,label,created_by) VALUES($1,$2,$3,$4,$5)`, sid, id.HouseholdID, in.ID, v.Label, id.UserID)
	if err != nil {
		return nil, err
	}
	for i, p := range v.Pages {
		if _, err = tx.Exec(ctx, `INSERT INTO documents.scan_pages(household_id,set_id,version_id,position,page_number) VALUES($1,$2,$3,$4,$5)`, id.HouseholdID, sid, p.VersionID, i+1, p.PageNumber); err != nil {
			return nil, err
		}
	}
	out["set_id"] = sid
	if in.Action == "scan_process" {
		raw, _ := json.Marshal(Record{"set_id": sid})
		queued, e := documentQueue(ctx, tx, id, in, "", "scan", raw)
		if e != nil {
			return nil, e
		}
		out["processing_id"] = queued["processing_id"]
	}
	return out, nil
}

func documentQueue(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand, versionID, operation string, input []byte) (Record, error) {
	if versionID != "" {
		if !value.ValidUUID(versionID) {
			return nil, Invalid{"Dateiversion prüfen"}
		}
		var found bool
		if err := tx.QueryRow(ctx, `SELECT true FROM documents.file_versions WHERE household_id=$1 AND document_id=$2 AND id=$3`, id.HouseholdID, in.ID, versionID).Scan(&found); err != nil {
			return nil, energyError(err)
		}
	} else if operation != "scan" {
		return nil, Invalid{"Dateiversion auswählen"}
	}
	pid, jid := value.UUID(), value.UUID()
	payload, _ := json.Marshal(Record{"processing_id": pid})
	_, err := tx.Exec(ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key) VALUES($1,$2,'documents',$3,'document_processing',$4,$5)`, jid, id.HouseholdID, in.Generation, payload, "document/"+pid)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO documents.processing(id,household_id,document_id,version_id,operation,input,job_id,module_generation,requested_by) VALUES($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,$9)`, pid, id.HouseholdID, in.ID, versionID, operation, input, jid, in.Generation, id.UserID)
	}
	return Record{"id": in.ID, "processing_id": pid}, err
}

func documentControlJob(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v struct {
		ProcessingID string `json:"processing_id"`
	}
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if !value.ValidUUID(v.ProcessingID) {
		return nil, Invalid{"Verarbeitung auswählen"}
	}
	var jid, state string
	err := tx.QueryRow(ctx, `SELECT job_id,state FROM documents.processing WHERE household_id=$1 AND document_id=$2 AND id=$3 AND module_generation=$4 FOR UPDATE`, id.HouseholdID, in.ID, v.ProcessingID, in.Generation).Scan(&jid, &state)
	if err != nil {
		return nil, energyError(err)
	}
	next, code := "cancelled", "cancelled_by_user"
	jobState := "paused"
	if in.Action == "retry" {
		if !slices.Contains([]string{"failed", "cancelled"}, state) {
			return nil, ErrConflict
		}
		next, code, jobState = "pending", "queued", "pending"
	} else if !slices.Contains([]string{"pending", "running"}, state) {
		return nil, ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE core.jobs SET state=$3,lease_token=NULL,lease_until=NULL,attempts=CASE WHEN $3='pending' THEN 0 ELSE attempts END,available_at=clock_timestamp(),last_error=NULL WHERE household_id=$1 AND id=$2`, id.HouseholdID, jid, jobState)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE documents.processing SET state=$3,status_code=$4,progress=0,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, v.ProcessingID, next, code)
	}
	return Record{"id": in.ID, "processing_id": v.ProcessingID, "state": next}, err
}

func (s *Store) documentIntegration(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	if id.Role != "admin" {
		return nil, ErrForbidden
	}
	var v struct {
		Adapter string `json:"adapter"`
		Enabled bool   `json:"enabled"`
	}
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"watch", "imap", "paperless"}, v.Adapter) {
		return nil, Invalid{"Eingangsadapter prüfen"}
	}
	var currentVersion int64
	err := tx.QueryRow(ctx, `SELECT version FROM documents.integration_sources WHERE household_id=$1 AND adapter=$2`, id.HouseholdID, v.Adapter).Scan(&currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if currentVersion != in.Version {
		return nil, ErrConflict
	}
	var rid string
	err = tx.QueryRow(ctx, `INSERT INTO documents.integration_sources(household_id,adapter,enabled,approved_by,module_generation) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(household_id,adapter) DO UPDATE SET enabled=excluded.enabled,approved_by=excluded.approved_by,module_generation=excluded.module_generation,version=documents.integration_sources.version+1,status_code='idle',next_poll_at=clock_timestamp() RETURNING id`, id.HouseholdID, v.Adapter, v.Enabled, id.UserID, in.Generation).Scan(&rid)
	return Record{"id": rid, "enabled": v.Enabled}, err
}
