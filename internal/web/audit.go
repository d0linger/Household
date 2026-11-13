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
	"unicode"
	"unicode/utf8"

	"household/internal/store"
	"household/internal/value"
)

type auditFilter struct {
	Module, Action, Resource, Request, Query string
	Before                                   int64
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
	BeforeLabel, AfterLabel    bool
}

type auditEntry struct {
	ID              int64                  `json:"id"`
	CreatedAt       time.Time              `json:"created_at"`
	ActorID         string                 `json:"actor_id"`
	ActorKind       string                 `json:"actor_kind"`
	ActorUsername   string                 `json:"actor_username,omitempty"`
	Module          string                 `json:"module"`
	Entity          string                 `json:"entity"`
	Resource        string                 `json:"resource"`
	Label           string                 `json:"label"`
	Action          string                 `json:"action"`
	Outcome         string                 `json:"outcome"`
	Request         string                 `json:"request_id"`
	EntityKey       json.RawMessage        `json:"entity_key"`
	Changes         map[string]auditChange `json:"changes"`
	Details         json.RawMessage        `json:"details"`
	TimeLabel       string                 `json:"-"`
	ModuleLabel     string                 `json:"-"`
	EntityLabel     string                 `json:"-"`
	ActionLabel     string                 `json:"-"`
	ActorLabel      string                 `json:"-"`
	Fields          []auditFieldView       `json:"-"`
	TechnicalFields []auditFieldView       `json:"-"`
	KeyLabel        string                 `json:"-"`
	Title           string                 `json:"-"`
	TitleLabel      bool                   `json:"-"`
	FieldCount      int                    `json:"-"`
}

type auditOption struct{ Value, Label string }

type auditResult struct {
	Entries  []auditEntry  `json:"entries"`
	Next     int64         `json:"next_before,omitempty"`
	Filter   auditFilter   `json:"-"`
	NextURL  string        `json:"-"`
	FirstURL string        `json:"-"`
	Actions  []auditOption `json:"-"`
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
	for _, action := range []string{"CREATE", "UPDATE", "DELETE", "login_success", "login_failed", "logout", "password_changed", "totp_enabled", "session_revoked", "module_enabled", "module_disabled"} {
		result.Actions = append(result.Actions, auditOption{Value: action, Label: auditActionLabel(action)})
	}
	if filter.Action != "" {
		found := false
		for _, option := range result.Actions {
			found = found || option.Value == filter.Action
		}
		if !found {
			result.Actions = append(result.Actions, auditOption{Value: filter.Action, Label: auditActionLabel(filter.Action)})
		}
	}
	if filter.Before != 0 {
		query := r.URL.Query()
		query.Del("before")
		result.FirstURL = "/settings/audit?" + query.Encode()
	}
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
		SELECT v.id,v.created_at,CASE WHEN legacy_recovery THEN '' ELSE coalesce(actor_user_id::text,'') END,actor_kind,
		display_module,display_entity,coalesce(display_resource::text,''),entity_label,action,outcome,
		coalesce(request_id::text,''),entity_key,changes,details,coalesce(actor.username,'') FROM visible v
		LEFT JOIN core.memberships member ON member.household_id=$1 AND member.user_id=v.actor_user_id
		 AND v.actor_kind IN('user','legacy') AND NOT v.legacy_recovery
		LEFT JOIN core.users actor ON actor.id=member.user_id
		WHERE ($2='' OR display_module=$2) AND ($3='' OR action=$3)
		AND ($4='' OR display_resource=nullif($4,'')::uuid) AND ($5='' OR request_id=nullif($5,'')::uuid)
		AND core.finance_audit_visible($1,$7,$8,$9,$10,v.id)
		AND ($11='' OR strpos(lower(entity_label),lower($11))>0 OR strpos(lower(coalesce(actor.username,'')),lower($11))>0)
		AND ($6::bigint=0 OR v.id<$6) ORDER BY v.id DESC LIMIT 51`,
		id.HouseholdID, filter.Module, filter.Action, filter.Resource, filter.Request, filter.Before,
		id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, filter.Query)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry auditEntry
		var raw []byte
		if err = rows.Scan(&entry.ID, &entry.CreatedAt, &entry.ActorID, &entry.ActorKind,
			&entry.Module, &entry.Entity, &entry.Resource, &entry.Label, &entry.Action, &entry.Outcome,
			&entry.Request, &entry.EntityKey, &raw, &entry.Details, &entry.ActorUsername); err != nil {
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
	f := auditFilter{Module: query.Get("module"), Action: query.Get("action"), Resource: query.Get("resource"), Request: query.Get("request"), Query: strings.TrimSpace(query.Get("q"))}
	invalid := store.Invalid{Message: "Protokollfilter prüfen: gültiges Modul, Aktion und UUID verwenden"}
	if f.Module != "" && f.Module != "core" {
		if _, ok := s.Store.Module(f.Module); !ok {
			return f, invalid
		}
	}
	if len(f.Action) > 64 || strings.ContainsAny(f.Action, " \t\n\r") ||
		!utf8.ValidString(f.Query) || utf8.RuneCountInString(f.Query) > 100 || strings.ContainsFunc(f.Query, unicode.IsControl) ||
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
	entry.TimeLabel = entry.CreatedAt.UTC().Format("02.01.2006 15:04:05")
	entry.ModuleLabel, entry.EntityLabel = "Früherer Bereich", auditEntityLabel(entry.Entity)
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
	entry.ActionLabel = auditEntryActionLabel(entry.Entity, entry.Action)
	entry.ActorLabel = map[string]string{"user": "Ehemaliger oder unbekannter Benutzer", "system": "System", "worker": "Hintergrunddienst", "operator": "Betreiber", "bootstrap": "Ersteinrichtung", "database": "Datenbank", "legacy": "Früheres Protokoll"}[entry.ActorKind]
	if entry.ActorLabel == "" {
		entry.ActorLabel = "Unbekannte Herkunft"
	}
	if entry.ActorKind == "legacy" && entry.Action == "operator_password_recovery" {
		entry.ActorLabel = "Früheres Betreiberereignis · ausführende Identität nicht erfasst"
		entry.ActorUsername = ""
	} else if entry.ActorKind != "user" && entry.ActorKind != "legacy" {
		entry.ActorUsername = ""
	}
	entry.Title, entry.TitleLabel = entry.Label, false
	fallback := entry.Module + "." + entry.Entity
	if entry.Label == "" || entry.Label == fallback || value.ValidUUID(entry.Resource) && entry.Label == fallback+" · "+entry.Resource[:8] {
		entry.Title, entry.TitleLabel = entry.EntityLabel, true
	}
	entry.KeyLabel = string(entry.EntityKey)
	entry.Fields, entry.TechnicalFields = nil, nil
	entry.FieldCount = len(entry.Changes)
	for name, change := range entry.Changes {
		label := labels[name]
		if label == "" {
			label = auditFieldLabel(name)
		}
		field := auditFieldView{Name: name, Label: label,
			Before: auditValue(change.Before, change.BeforePresent, change.Redacted, kinds[name]),
			After:  auditValue(change.After, change.AfterPresent, change.Redacted, kinds[name]), Redacted: change.Redacted,
			BeforeLabel: auditValueLabel(change.Before, change.BeforePresent, change.Redacted),
			AfterLabel:  auditValueLabel(change.After, change.AfterPresent, change.Redacted)}
		if auditTechnicalField(name, change) {
			entry.TechnicalFields = append(entry.TechnicalFields, field)
		} else {
			entry.Fields = append(entry.Fields, field)
		}
	}
	sort.Slice(entry.Fields, func(i, j int) bool { return entry.Fields[i].Name < entry.Fields[j].Name })
	sort.Slice(entry.TechnicalFields, func(i, j int) bool { return entry.TechnicalFields[i].Name < entry.TechnicalFields[j].Name })
}

func auditValueLabel(v any, present, redacted bool) bool {
	_, boolean := v.(bool)
	return !present || redacted || v == nil || boolean
}

func auditTechnicalField(name string, change auditChange) bool {
	return name == "id" || name == "version" || name == "auth_version" || name == "data_generation" || name == "state_version" ||
		strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "_by") ||
		value.ValidUUID(store.String(change.Before)) || value.ValidUUID(store.String(change.After))
}

func auditEntityLabel(entity string) string {
	switch entity {
	case "users":
		return "Benutzerkonto"
	case "memberships":
		return "Haushaltsmitgliedschaft"
	case "households":
		return "Haushalt"
	case "user_preferences", "member_preferences":
		return "Persönliche Einstellungen"
	case "sessions":
		return "Sitzung"
	case "totp_credentials":
		return "Zweiter Faktor"
	case "recovery_codes":
		return "Wiederherstellungscode"
	case "passkey_credentials", "passkeys":
		return "Passkey"
	case "reauthentication":
		return "Anmeldebestätigung"
	case "module_states":
		return "Modulstatus"
	case "module_purge_requests":
		return "Modullöschung"
	case "resource_links":
		return "Verknüpfung"
	case "events":
		return "Ereignis"
	case "attachments":
		return "Anhang"
	case "attachment_derivatives":
		return "Anhangsvorschau"
	case "capture_jobs":
		return "Erfassung"
	case "reminders":
		return "Erinnerung"
	case "notifications":
		return "Mitteilung"
	case "push_subscriptions":
		return "Push-Mitteilungen"
	case "import_batches":
		return "Import"
	case "import_rows":
		return "Importzeile"
	case "backup_runs":
		return "Sicherung"
	case "jobs":
		return "Hintergrundaufgabe"
	case "item_events":
		return "Einkaufsaktivität"
	case "checkouts":
		return "Einkaufsabschluss"
	case "recipe_tags":
		return "Rezeptzuordnung"
	case "versions":
		return "Rezeptversion"
	case "cooking_sessions":
		return "Kochvorgang"
	case "cooking_steps":
		return "Kochschritt"
	case "cooking_timers":
		return "Kochtimer"
	case "purchases":
		return "Einkaufszuordnung"
	case "participants":
		return "Teilnehmer"
	case "reservations":
		return "Reservierung"
	case "areas":
		return "Aufgabenbereich"
	case "series_profiles":
		return "Aufgabenserie"
	case "template_versions":
		return "Aufgabenversion"
	case "occurrence_details":
		return "Aufgabentermin"
	case "occurrence_checklist_items":
		return "Checklistenpunkt"
	case "version_checklist_items":
		return "Checklistenvorlage"
	case "version_dependencies":
		return "Aufgabenabhängigkeit"
	case "rotation_members":
		return "Aufgabenrotation"
	case "completions":
		return "Erledigung"
	case "absences":
		return "Abwesenheit"
	case "calendar_feeds":
		return "Kalenderfreigabe"
	case "reminder_deliveries":
		return "Erinnerungsversand"
	case "schedule_state":
		return "Terminplanung"
	case "registers":
		return "Zählwerk"
	case "device_registers":
		return "Gerätezuordnung"
	case "reading_revisions":
		return "Ableseversion"
	case "device_changes":
		return "Zählerwechsel"
	case "tariff_versions":
		return "Tarifversion"
	case "payments":
		return "Zahlung"
	case "bills":
		return "Abrechnung"
	case "reading_plans":
		return "Ableseplan"
	case "plan_completions":
		return "Ablesetermin"
	case "integration_sources":
		return "Datenquelle"
	case "telemetry":
		return "Messwert"
	case "intervals":
		return "Messintervall"
	case "aggregates":
		return "Messwertzusammenfassung"
	case "dirty_ranges":
		return "Neuberechnung"
	case "command_receipts":
		return "Vorgang"
	case "profiles":
		return "Dokumentprüfung"
	case "file_versions":
		return "Dateiversion"
	case "scan_sets":
		return "Dokumentenscan"
	case "scan_pages":
		return "Scanseite"
	case "deadlines":
		return "Dokumentfrist"
	case "saved_views":
		return "Gespeicherte Ansicht"
	case "rules":
		return "Dokumentregel"
	case "processing":
		return "Dokumentverarbeitung"
	case "import_receipts":
		return "Importbeleg"
	case "cooking_stock_postings":
		return "Kochbuchung"
	case "expense_edit_operations":
		return "Ausgabenkorrektur"
	case "financial_policies":
		return "Finanzfreigabe"
	case "financial_subjects":
		return "Finanzzuordnung"
	case "ingredient_product_bindings":
		return "Zutatenzuordnung"
	case "meal_reservation_allocations":
		return "Reservierungszuordnung"
	case "meal_reservations":
		return "Mahlzeitenreservierung"
	case "planning_ingredient_bindings":
		return "Planungszutat"
	case "planning_purchase_operations":
		return "Planungseinkauf"
	case "planning_purchase_requirements":
		return "Einkaufsbedarf"
	case "account_movements":
		return "Kontobewegung"
	case "accounts":
		return "Konto"
	case "bank_profiles":
		return "Bankimportprofil"
	case "cash_events":
		return "Zahlungsereignis"
	case "categorization_rules":
		return "Kategorieregel"
	case "category_budgets":
		return "Kategoriebudget"
	case "entry_finance":
		return "Finanzdetails"
	case "entry_original_lines":
		return "Ursprüngliche Aufteilung"
	case "entry_person_shares":
		return "Persönlicher Anteil"
	case "entry_relations":
		return "Ausgabenverknüpfung"
	case "entry_split_sets":
		return "Ausgabenaufteilung"
	case "goal_contributions":
		return "Sparbeitrag"
	case "receipt_review_lines":
		return "Belegposition"
	case "receipt_reviews":
		return "Belegprüfung"
	case "reconciliation_items":
		return "Abgleichposition"
	case "reconciliations":
		return "Kontoabgleich"
	case "recurring_occurrences":
		return "Wiederkehrende Buchung"
	case "recurring_templates":
		return "Buchungsvorlage"
	case "saved_filters":
		return "Gespeicherter Filter"
	case "savings_goals":
		return "Sparziel"
	case "settlement_details":
		return "Ausgleich"
	case "calendar_tombstones":
		return "Entfernter Kalendertermin"
	case "components":
		return "Mahlzeitenbestandteil"
	case "leftover_allocations":
		return "Restezuordnung"
	case "leftover_sources":
		return "Restemenge"
	case "weekly_settings":
		return "Wochenplanung"
	case "cooking_stock_movements":
		return "Vorratsverbrauch beim Kochen"
	case "cooking_stock_receipts":
		return "Kochverbrauchsbeleg"
	case "stock_batch_drafts":
		return "Vorratserfassung"
	case "stock_batch_lines":
		return "Vorratsposition"
	case "stock_batch_receipts":
		return "Vorratsbuchungsbeleg"
	case "stock_operations":
		return "Vorratsvorgang"
	case "cooking_records":
		return "Kochhistorie"
	case "planning_purchase_receipts":
		return "Planungseinkaufsbeleg"
	default:
		return "Datensatz"
	}
}

func auditFieldLabel(name string) string {
	switch name {
	case "name":
		return "Name"
	case "title":
		return "Titel"
	case "description":
		return "Beschreibung"
	case "notes":
		return "Notizen"
	case "note":
		return "Notiz"
	case "state":
		return "Status"
	case "role":
		return "Rolle"
	case "theme":
		return "Darstellung"
	case "module_order":
		return "Modulreihenfolge"
	case "locale":
		return "Sprache"
	case "timezone":
		return "Zeitzone"
	case "currency_code":
		return "Währung"
	case "offline_storage_enabled":
		return "Offline-Speicherung"
	case "disabled_at":
		return "Gesperrt am"
	case "inactive_at":
		return "Deaktiviert am"
	case "archived_at":
		return "Archiviert am"
	case "enabled_at":
		return "Aktiviert am"
	case "revoked_at":
		return "Beendet am"
	case "password_hash":
		return "Passwort"
	case "password_changed_at":
		return "Passwort geändert am"
	case "must_change_password":
		return "Passwortwechsel erforderlich"
	case "username", "username_key":
		return "Benutzername"
	case "display_name":
		return "Anzeigename"
	case "email":
		return "E-Mail-Adresse"
	case "encrypted_secret":
		return "Geheimer Schlüssel"
	case "token_hash":
		return "Zugangsschlüssel"
	case "code_hash":
		return "Wiederherstellungscode"
	case "consumed_at":
		return "Verwendet am"
	case "absolute_expires_at":
		return "Läuft spätestens ab"
	case "reauthenticated_at":
		return "Anmeldung bestätigt am"
	case "checked_at":
		return "Abgehakt am"
	case "completed_at":
		return "Erledigt am"
	case "posted_at":
		return "Gebucht am"
	case "confirmed_at":
		return "Bestätigt am"
	case "changed_at":
		return "Geändert am"
	case "expires_at":
		return "Läuft ab"
	case "finished_at":
		return "Abgeschlossen am"
	case "started_at":
		return "Begonnen am"
	case "sort_order":
		return "Reihenfolge"
	case "position":
		return "Position"
	case "amount_minor":
		return "Betrag in Cent"
	case "quantity":
		return "Menge"
	case "quantity_delta":
		return "Mengenänderung"
	case "kind":
		return "Art"
	case "active":
		return "Aktiv"
	case "enabled":
		return "Aktiviert"
	case "module_id":
		return "Modulkennung"
	case "id":
		return "Datensatzkennung"
	case "household_id":
		return "Haushaltskennung"
	case "user_id":
		return "Benutzerkennung"
	case "created_by":
		return "Erstellt von"
	case "version":
		return "Version"
	case "auth_version":
		return "Anmeldeversion"
	case "data_generation":
		return "Datengeneration"
	case "state_version":
		return "Statusversion"
	default:
		return strings.ReplaceAll(name, "_", " ")
	}
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

func auditEntryActionLabel(entity, action string) string {
	switch entity + "/" + action {
	case "passkeys/record_write":
		return "Passkey gespeichert"
	case "passkeys/record_delete":
		return "Passkey entfernt"
	case "passkeys/login_success":
		return "Mit Passkey angemeldet"
	case "reauthentication/login_success":
		return "Anmeldung bestätigt"
	case "totp_credentials/record_delete":
		return "Zweiter Faktor deaktiviert"
	case "recovery_codes/record_write":
		return "Wiederherstellungscodes erneuert"
	default:
		return auditActionLabel(action)
	}
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
		"module_enabled": "Modul aktiviert", "module_disabled": "Modul pausiert", "module_purged": "Moduldaten gelöscht",
		"record_write": "Speichern bestätigt", "record_delete": "Löschen bestätigt", "attachment_upload": "Anhang hochgeladen",
		"import_confirm": "Import bestätigt", "capture_confirm": "Erfassung bestätigt", "user_create": "Benutzer angelegt", "user_update": "Benutzer geändert",
		"archive": "Archiviert", "check": "Abgehakt", "post": "Gebucht", "reverse": "Storniert", "complete": "Abgeschlossen", "shopping": "Einkauf übertragen",
		"tags": "Zuordnungen geändert", "recipe": "Rezept übernommen", "expense": "Ausgabe übernommen", "transfer": "Übertragen", "bootstrap": "Haushalt eingerichtet",
		"worker_job_failed": "Hintergrundaufgabe fehlgeschlagen", "worker_job_unsupported": "Hintergrundaufgabe nicht unterstützt", "worker_lease_exhausted": "Hintergrundaufgabe abgebrochen",
	}
	if label, ok := labels[action]; ok {
		return label
	}
	return "Ereignis"
}
