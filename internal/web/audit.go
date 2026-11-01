package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"household/internal/store"
	"household/internal/value"
)

type auditFilter struct {
	Module, Action, Resource, Request string
	Before                            int64
}

type auditChange struct {
	Before        any  `json:"before"`
	After         any  `json:"after"`
	BeforePresent bool `json:"before_present"`
	AfterPresent  bool `json:"after_present"`
	Redacted      bool `json:"redacted"`
}

type auditFieldView struct {
	Name, Label, Before, After string
	Redacted                   bool
}

type auditEntry struct {
	ID          int64                  `json:"id"`
	CreatedAt   time.Time              `json:"created_at"`
	ActorID     string                 `json:"actor_id"`
	ActorKind   string                 `json:"actor_kind"`
	Module      string                 `json:"module"`
	Entity      string                 `json:"entity"`
	Resource    string                 `json:"resource"`
	Label       string                 `json:"label"`
	Action      string                 `json:"action"`
	Outcome     string                 `json:"outcome"`
	Request     string                 `json:"request_id"`
	EntityKey   json.RawMessage        `json:"entity_key"`
	Changes     map[string]auditChange `json:"changes"`
	Details     json.RawMessage        `json:"details"`
	TimeLabel   string                 `json:"-"`
	ModuleLabel string                 `json:"-"`
	EntityLabel string                 `json:"-"`
	ActionLabel string                 `json:"-"`
	ActorLabel  string                 `json:"-"`
	Fields      []auditFieldView       `json:"-"`
	KeyLabel    string                 `json:"-"`
}

type auditResult struct {
	Entries []auditEntry `json:"entries"`
	Next    int64        `json:"next_before,omitempty"`
	Filter  auditFilter  `json:"-"`
	NextURL string       `json:"-"`
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request) {
	result, err := s.readAudit(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Änderungsprotokoll")
	p.View, p.Extra = "audit", result
	s.render(w, r, p)
}

func (s *Server) auditAPI(w http.ResponseWriter, r *http.Request) {
	result, err := s.readAudit(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) readAudit(r *http.Request) (auditResult, error) {
	result := auditResult{Entries: []auditEntry{}}
	id := identity(r)
	if id.Role != "admin" {
		return result, store.ErrForbidden
	}
	filter, err := s.parseAuditFilter(r.URL.Query())
	if err != nil {
		return result, err
	}
	result.Filter = filter
	// The previous recovery command left three scope columns NULL and recorded
	// the target account as actor. Project only that known legacy shape into the
	// installation's singleton household; do not expose arbitrary unscoped rows.
	rows, err := s.Pool.Query(r.Context(), `WITH scoped AS (
		SELECT a.*,actor_kind='legacy' AND action='operator_password_recovery'
		AND household_id IS NULL AND module_id IS NULL AND resource_type IS NULL
		AND resource_id IS NULL AND actor_user_id IS NOT NULL AS legacy_recovery FROM core.audit_log a),
		visible AS (SELECT *,coalesce(module_id,'core') AS display_module,
		coalesce(resource_type,CASE WHEN legacy_recovery THEN 'users' ELSE 'events' END) AS display_entity,
		CASE WHEN legacy_recovery THEN actor_user_id ELSE resource_id END AS display_resource
		FROM scoped WHERE household_id=$1 OR (legacy_recovery AND
		EXISTS(SELECT 1 FROM core.households WHERE id=$1 AND singleton=1)))
		SELECT id,created_at,CASE WHEN legacy_recovery THEN '' ELSE coalesce(actor_user_id::text,'') END,actor_kind,
		display_module,display_entity,coalesce(display_resource::text,''),entity_label,action,outcome,
		coalesce(request_id::text,''),entity_key,changes,details FROM visible
		WHERE ($2='' OR display_module=$2) AND ($3='' OR action=$3)
		AND ($4='' OR display_resource=nullif($4,'')::uuid) AND ($5='' OR request_id=nullif($5,'')::uuid)
		AND core.finance_audit_visible($1,$7,$8,$9,$10,id)
		AND ($6::bigint=0 OR id<$6) ORDER BY id DESC LIMIT 51`,
		id.HouseholdID, filter.Module, filter.Action, filter.Resource, filter.Request, filter.Before,
		id.UserID, id.MembershipID, id.SessionID, id.AuthVersion)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry auditEntry
		var raw []byte
		if err = rows.Scan(&entry.ID, &entry.CreatedAt, &entry.ActorID, &entry.ActorKind,
			&entry.Module, &entry.Entity, &entry.Resource, &entry.Label, &entry.Action, &entry.Outcome,
			&entry.Request, &entry.EntityKey, &raw, &entry.Details); err != nil {
			return result, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&entry.Changes); err != nil {
			return result, err
		}
		s.describeAudit(&entry)
		result.Entries = append(result.Entries, entry)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(result.Entries) > 50 {
		result.Entries = result.Entries[:50]
		result.Next = result.Entries[49].ID
		query := r.URL.Query()
		query.Set("before", strconv.FormatInt(result.Next, 10))
		result.NextURL = "/settings/audit?" + query.Encode()
	}
	return result, nil
}

func (s *Server) parseAuditFilter(query url.Values) (auditFilter, error) {
	f := auditFilter{Module: query.Get("module"), Action: query.Get("action"), Resource: query.Get("resource"), Request: query.Get("request")}
	invalid := store.Invalid{Message: "Protokollfilter prüfen: gültiges Modul, Aktion und UUID verwenden"}
	if f.Module != "" && f.Module != "core" {
		if _, ok := s.Store.Module(f.Module); !ok {
			return f, invalid
		}
	}
	if len(f.Action) > 64 || strings.ContainsAny(f.Action, " \t\n\r") ||
		(f.Resource != "" && !value.ValidUUID(f.Resource)) || (f.Request != "" && !value.ValidUUID(f.Request)) {
		return f, invalid
	}
	if before := query.Get("before"); before != "" {
		var err error
		f.Before, err = strconv.ParseInt(before, 10, 64)
		if err != nil || f.Before < 1 {
			return f, invalid
		}
	}
	return f, nil
}

func (s *Server) describeAudit(entry *auditEntry) {
	entry.TimeLabel = entry.CreatedAt.UTC().Format("02.01.2006 · 15:04:05 UTC")
	entry.ModuleLabel, entry.EntityLabel = entry.Module, entry.Entity
	labels, kinds := map[string]string{}, map[string]string{}
	if entry.Module == "core" {
		entry.ModuleLabel = "Haushalt & Sicherheit"
	}
	if m, ok := s.Store.Module(entry.Module); ok {
		entry.ModuleLabel = m.Title
		if e, found := m.Entity(entry.Entity); found {
			entry.EntityLabel = e.Title
			for _, field := range e.Fields {
				labels[field.Name], kinds[field.Name] = field.Label, field.Kind
			}
		}
	}
	entry.ActionLabel = auditActionLabel(entry.Action)
	entry.ActorLabel = map[string]string{"user": "Benutzer", "system": "System", "worker": "Hintergrunddienst", "operator": "Betreiber", "bootstrap": "Ersteinrichtung", "database": "Datenbank", "legacy": "Früheres Protokoll"}[entry.ActorKind]
	if entry.ActorLabel == "" {
		entry.ActorLabel = "Unbekannte Herkunft"
	}
	if entry.ActorKind == "legacy" && entry.Action == "operator_password_recovery" {
		entry.ActorLabel = "Früheres Betreiberereignis · ausführende Identität nicht erfasst"
	} else if entry.ActorID != "" {
		entry.ActorLabel += " · " + entry.ActorID
	}
	entry.KeyLabel = string(entry.EntityKey)
	for name, change := range entry.Changes {
		label := labels[name]
		if label == "" {
			label = name
		}
		entry.Fields = append(entry.Fields, auditFieldView{Name: name, Label: label,
			Before: auditValue(change.Before, change.BeforePresent, change.Redacted, kinds[name]),
			After:  auditValue(change.After, change.AfterPresent, change.Redacted, kinds[name]), Redacted: change.Redacted})
	}
	sort.Slice(entry.Fields, func(i, j int) bool { return entry.Fields[i].Name < entry.Fields[j].Name })
}

func auditValue(v any, present, redacted bool, kind string) string {
	if !present {
		return "Nicht vorhanden"
	}
	if redacted {
		return "Geschützt"
	}
	if v == nil {
		return "Leer"
	}
	if kind == "money" {
		return formatMoney(v)
	}
	if kind == "quantity" {
		return formatQuantity(v)
	}
	if b, ok := v.(bool); ok {
		if b {
			return "Ja"
		}
		return "Nein"
	}
	return store.String(v)
}

func auditActionLabel(action string) string {
	// #nosec G101 -- Public UI labels for action names, never credential values.
	labels := map[string]string{
		"CREATE": "Angelegt", "UPDATE": "Geändert", "DELETE": "Gelöscht",
		"create": "Anlegen bestätigt", "update": "Änderung bestätigt", "delete": "Löschen bestätigt",
		"login_success": "Anmeldung", "login_failed": "Anmeldung abgelehnt", "logout": "Abmeldung",
		"password_changed": "Passwort geändert", "totp_setup": "Zweiter Faktor vorbereitet",
		"totp_enabled": "Zweiter Faktor aktiviert", "recovery_code_used": "Wiederherstellungscode verwendet",
		"session_revoked": "Sitzung beendet", "operator_password_recovery": "Konto durch Betreiber wiederhergestellt",
		"activity": "Einkaufsstatus geändert", "clone": "Neue Liste aus Vorlage erstellt", "merge": "Einkaufsartikel zusammengeführt",
	}
	if label, ok := labels[action]; ok {
		return label
	}
	return action
}
