package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// These adapters are the only Fuel boundary that accesses another module's facts.
// A durable binding remains authoritative when a disposable Core link is pruned.
func fuelModuleCoreAuthorizeTarget(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, rid string, generation int64, lock bool) error {
	if !value.ValidUUID(rid) || generation <= 0 {
		return ErrNotFound
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.module_states WHERE household_id=$1 AND module_id=$2 AND state='enabled' AND data_generation=$3)`, id.HouseholdID, mid, generation).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return ErrNotFound
	}
	if mid == "expenses" && entity == "entries" {
		if lock {
			return lockFinancialReplay(ctx, tx, id, "entries", rid, financialView)
		}
		scope, err := readFinancialScope(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = readFinancialAccessVersion(ctx, tx, scope, rid)
		return err
	}
	var query string
	switch mid + "/" + entity {
	case "chores/task_occurrences":
		query = `SELECT id FROM chores.task_occurrences WHERE household_id=$1 AND id=$2`
	case "documents/documents":
		query = `SELECT id FROM documents.documents WHERE household_id=$1 AND id=$2 AND archived_at IS NULL`
	default:
		return ErrNotFound
	}
	if lock {
		query += " FOR SHARE NOWAIT"
	}
	var found string
	err = tx.QueryRow(ctx, query, id.HouseholdID, rid).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func fuelModuleCoreAssignee(ctx context.Context, tx pgx.Tx, id auth.Identity, assignee string) error {
	if assignee == "" {
		return nil
	}
	if !value.ValidUUID(assignee) {
		return Invalid{"Zuständiges Haushaltsmitglied erneut auswählen"}
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT true FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.id=$2 AND m.inactive_at IS NULL AND u.disabled_at IS NULL FOR SHARE OF m,u NOWAIT`, id.HouseholdID, assignee).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !active {
		return ErrNotFound
	}
	return nil
}

func fuelModuleCoreBinding(ctx context.Context, tx pgx.Tx, id auth.Identity, source, rid, mid, relation string, cycle int64) (Record, bool, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT to_jsonb(b) FROM fuel.integration_bindings b WHERE household_id=$1
 AND source_entity=$2 AND source_id=$3 AND source_cycle=$4 AND target_module=$5 AND relation=$6 FOR UPDATE NOWAIT`, id.HouseholdID, source, rid, cycle, mid, relation).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	binding, err := decode(raw)
	if err != nil {
		return nil, false, err
	}
	if binding["state"] != "linked" {
		return nil, true, Invalid{"Die ursprüngliche Verknüpfung ist erhalten, ihr Ziel ist derzeit nicht nutzbar. Keine zweite Buchung anlegen"}
	}
	err = fuelModuleCoreAuthorizeTarget(ctx, tx, id, mid, String(binding["target_entity"]), String(binding["target_id"]), Int(binding["target_generation"]), true)
	return binding, true, err
}

func fuelModuleCoreSaveBinding(ctx context.Context, tx pgx.Tx, id auth.Identity, source, rid string, cycle int64, mid, entity, target, relation, vehicle string, generation int64) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `INSERT INTO fuel.integration_bindings AS b(household_id,vehicle_id,source_entity,source_id,source_cycle,target_module,target_entity,target_id,target_generation,relation,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING to_jsonb(b)`, id.HouseholdID, vehicle, source, rid, cycle, mid, entity, target, generation, relation, id.UserID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'fuel',$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, id.HouseholdID, source, rid, mid, entity, target, relation); err != nil {
		return nil, err
	}
	return decode(raw)
}

func (s *Store) fuelModuleCoreExpense(ctx context.Context, tx pgx.Tx, id auth.Identity, source, rid string, data Record, generation int64) (Record, error) {
	current, err := requireModule(ctx, tx, id, "expenses")
	if err != nil {
		return nil, err
	}
	if current != generation {
		return nil, ErrConflict
	}
	if binding, exists, err := fuelModuleCoreBinding(ctx, tx, id, source, rid, "expenses", "fuel_expense", 0); exists || err != nil {
		return binding, err
	}
	// Adopt one pre-module legacy link rather than recreating its financial effect.
	if source == "fillups" {
		legacy, exists, err := existingFuelExpense(ctx, tx, id, rid)
		if err != nil {
			return nil, err
		}
		if exists {
			return fuelModuleCoreSaveBinding(ctx, tx, id, source, rid, 0, "expenses", "entries", legacy, "fuel_expense", String(data["vehicle_id"]), generation)
		}
	}
	var currency string
	if err = tx.QueryRow(ctx, `SELECT currency_code FROM core.households WHERE id=$1`, id.HouseholdID).Scan(&currency); err != nil {
		return nil, err
	}
	if String(data["currency_code"]) != currency || Int(data["total_minor"]) <= 0 {
		return nil, Invalid{"Nur einen bekannten positiven Betrag in Haushaltswährung als Ausgabe übernehmen; keine automatische Umrechnung"}
	}
	category := String(data["expense_category_id"])
	if !value.ValidUUID(category) {
		return nil, Invalid{"Ausgabenkategorie auswählen"}
	}
	var categoryExists bool
	if err = tx.QueryRow(ctx, `SELECT true FROM expenses.categories WHERE household_id=$1 AND id=$2 AND archived_at IS NULL FOR SHARE NOWAIT`, id.HouseholdID, category).Scan(&categoryExists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !categoryExists {
		return nil, ErrNotFound
	}
	m, _ := s.Module("expenses")
	e, _ := m.Entity("entries")
	title := String(data["title"])
	if title == "" {
		title = "Fahrzeugbeleg: " + String(data["station"])
	}
	if chars := []rune(title); len(chars) > 300 {
		title = string(chars[:300])
	}
	entryID := value.UUID()
	financeScope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	sourceKind := "shared_fuel_fillups"
	if source == "vehicle_costs" {
		sourceKind = "shared_fuel_vehicle_costs"
	} else if source != "fillups" {
		return nil, ErrNotFound
	}
	financeSources := []FinanceResourceRef{{Kind: sourceKind, ID: rid}}
	if replacement := String(data["replaces_entry_id"]); replacement != "" {
		financeSources = append(financeSources, FinanceResourceRef{Kind: "entries", ID: replacement})
	}
	financeOperation, err := prepareFinanceOrigin(ctx, tx, id, financeScope, "entries", entryID, "fuel", financeSources)
	if err != nil {
		return nil, err
	}
	entry, err := save(ctx, tx, id, m, e, entryID, Record{"occurred_on": data["occurred_on"], "description": title, "payee": String(data["station"]), "amount_minor": Int(data["total_minor"]), "kind": "expense"}, true, 0)
	if err != nil {
		return nil, err
	}
	if replacement := String(data["replaces_entry_id"]); replacement != "" {
		if !value.ValidUUID(replacement) {
			return nil, ErrForbidden
		}
		if _, err = tx.Exec(ctx, `UPDATE expenses.entries SET replaces_entry_id=$3,version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND state='draft'`, id.HouseholdID, entryID, replacement); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,created_by) VALUES($1,$2,$3,$4,$5)`, id.HouseholdID, entryID, category, Int(data["total_minor"]), id.UserID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID); err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, entry, generation); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, financeScope, financeOperation); err != nil {
		return nil, err
	}
	return fuelModuleCoreSaveBinding(ctx, tx, id, source, rid, 0, "expenses", "entries", entryID, "fuel_expense", String(data["vehicle_id"]), generation)
}

func (s *Store) fuelModuleCoreCorrectExpense(ctx context.Context, tx pgx.Tx, id auth.Identity, sourceID string, generation int64, apply bool) (string, error) {
	current, err := requireModule(ctx, tx, id, "expenses")
	if err != nil {
		return "", err
	}
	if current != generation {
		return "", ErrConflict
	}
	binding, exists, err := fuelModuleCoreBinding(ctx, tx, id, "vehicle_costs", sourceID, "expenses", "fuel_expense", 0)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", Invalid{"Keine eindeutig verknüpfte ursprüngliche Ausgabe vorhanden"}
	}
	entryID := String(binding["target_id"])
	if err = lockFinancialReplay(ctx, tx, id, "entries", entryID, financialEdit); err != nil {
		return "", err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT to_jsonb(e) FROM expenses.entries e WHERE household_id=$1 AND id=$2 AND state='posted' AND kind='expense' FOR UPDATE NOWAIT`, id.HouseholdID, entryID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrConflict
	}
	if err != nil {
		return "", err
	}
	entry, err := decode(raw)
	if err != nil {
		return "", err
	}
	var reversed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entries WHERE household_id=$1 AND reverses_entry_id=$2)`, id.HouseholdID, entryID).Scan(&reversed); err != nil {
		return "", err
	}
	if reversed {
		return "", Invalid{"Die ursprüngliche Ausgabe wurde bereits separat storniert. Bestehende Finanzkorrektur zuerst abgleichen"}
	}
	var lineCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM(SELECT id FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2 LIMIT 101) bounded`, id.HouseholdID, entryID).Scan(&lineCount); err != nil {
		return "", err
	}
	if lineCount < 1 || lineCount > 100 {
		return "", Invalid{"Die ursprüngliche Ausgabe hat keine vollständig begrenzte Aufteilung"}
	}
	if !apply {
		return entryID, nil
	}
	reversalID := value.UUID()
	financeScope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return "", err
	}
	financeOperation, err := prepareFinanceOrigin(ctx, tx, id, financeScope, "entries", reversalID, "correction", []FinanceResourceRef{{Kind: "entries", ID: entryID}})
	if err != nil {
		return "", err
	}
	description := []rune("Storno Fahrzeugkosten: " + String(entry["description"]))
	if len(description) > 300 {
		description = description[:300]
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,payee,description,amount_minor,kind,reverses_entry_id,created_by)
 SELECT $1,$2,(transaction_timestamp() AT TIME ZONE h.timezone)::date,$3,$4,$5,'reversal',$6,$7 FROM core.households h WHERE h.id=$2`,
		reversalID, id.HouseholdID, String(entry["payee"]), string(description), -Int(entry["amount_minor"]), entryID, id.UserID)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
 SELECT household_id,$3,category_id,-amount_minor,note,position,$4 FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, entryID, reversalID, id.UserID)
	if err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, `UPDATE expenses.entries AS e SET state='posted',posted_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(e)`, id.HouseholdID, reversalID).Scan(&raw)
	if err != nil {
		return "", err
	}
	reversal, err := decode(raw)
	if err != nil {
		return "", err
	}
	m, _ := s.Module("expenses")
	e, _ := m.Entity("entries")
	if err = s.afterWrite(ctx, tx, id, m, e, reversal, generation); err != nil {
		return "", err
	}
	if err = completeFinanceOrigin(ctx, tx, id, financeScope, financeOperation); err != nil {
		return "", err
	}
	return entryID, nil
}

func (s *Store) fuelModuleCoreTask(ctx context.Context, tx pgx.Tx, id auth.Identity, source, rid string, data Record, generation int64) (Record, error) {
	current, err := requireModule(ctx, tx, id, "chores")
	if err != nil {
		return nil, err
	}
	if current != generation {
		return nil, ErrConflict
	}
	cycle := Int(data["cycle"])
	if binding, exists, err := fuelModuleCoreBinding(ctx, tx, id, source, rid, "chores", "maintenance_task", cycle); exists || err != nil {
		return binding, err
	}
	if data["active"] != true || data["task_opt_in"] != true || cycle <= 0 {
		return nil, Invalid{"Aufgabenverknüpfung für den aktiven Wartungsplan ausdrücklich freigeben"}
	}
	if err = fuelModuleCoreAssignee(ctx, tx, id, String(data["assignee_id"])); err != nil {
		return nil, err
	}
	var timezone, today string
	if err = tx.QueryRow(ctx, `SELECT timezone,(transaction_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1`, id.HouseholdID).Scan(&timezone, &today); err != nil {
		return nil, err
	}
	day := String(data["due_on"])
	kmReached := false
	if String(data["due_odometer"]) != "" && String(data["odometer_series_id"]) != "" {
		// A kilometer plan is actionable now only when its measured threshold is met.
		reading, err := fuelCurrentReading(ctx, tx, id, String(data["vehicle_id"]), today)
		if err != nil {
			return nil, err
		}
		if (String(reading["series_id"]) != String(data["odometer_series_id"]) || reading["reading"] == nil) && day == "" {
			return nil, Invalid{"Für diese Wartungsreihe ist kein eindeutiger aktueller Stand bekannt"}
		}
		target, err := fuelModuleDecimal(String(data["due_odometer"]), false)
		if err != nil {
			return nil, err
		}
		if reading["reading"] != nil && String(reading["series_id"]) == String(data["odometer_series_id"]) {
			actual, err := fuelModuleDecimal(String(reading["reading"]), false)
			if err != nil {
				return nil, err
			}
			kmReached = actual.Cmp(target) >= 0
		}
	}
	if day == "" && !kmReached {
		return nil, Invalid{"Kilometerfälligkeit ist noch nicht eindeutig erreicht; Datum festlegen oder aktuellen Stand prüfen"}
	}
	if day == "" || kmReached && day > today {
		day = today
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	date, err := time.ParseInLocation(time.DateOnly, day, loc)
	if err != nil {
		return nil, err
	}
	due := time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, loc)
	m, _ := s.Module("chores")
	occurrenceEntity, _ := m.Entity("task_occurrences")
	templateID, occurrenceID := value.UUID(), value.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO chores.task_templates(id,household_id,title,description,assignee_id,timezone,active,created_by)
 VALUES($1,$2,$3,$4,$5,$6,true,$7)`, templateID, id.HouseholdID, data["title"], "Wartungsplan in Fahrzeuge & Tanken: /m/fuel/maintenance_plans/"+rid, nullableFuel(data, "assignee_id"), timezone, id.UserID)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `INSERT INTO chores.task_occurrences AS t(id,household_id,template_id,occurrence_key,due_at,assignee_id,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(t)`, occurrenceID, id.HouseholdID, templateID, "fuel/"+rid+"/"+strconv.FormatInt(cycle, 10), due, nullableFuel(data, "assignee_id"), id.UserID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	occurrence, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, occurrenceEntity, occurrence, generation); err != nil {
		return nil, err
	}
	return fuelModuleCoreSaveBinding(ctx, tx, id, source, rid, cycle, "chores", "task_occurrences", occurrenceID, "maintenance_task", String(data["vehicle_id"]), generation)
}

func (s *Store) fuelModuleCoreDocument(ctx context.Context, tx pgx.Tx, id auth.Identity, source, rid string, data Record, generation int64) (Record, error) {
	target := String(data["document_id"])
	if err := fuelModuleCoreAuthorizeTarget(ctx, tx, id, "documents", "documents", target, generation, true); err != nil {
		return nil, err
	}
	// Relation carries target identity: one vehicle can own several independent documents.
	relation := "vehicle_document/" + target
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT to_jsonb(b) FROM fuel.integration_bindings b WHERE household_id=$1 AND source_entity=$2 AND source_id=$3
 AND source_cycle=0 AND target_module='documents' AND relation=$4 FOR UPDATE NOWAIT`, id.HouseholdID, source, rid, relation).Scan(&raw)
	if err == nil {
		binding, err := decode(raw)
		if err != nil {
			return nil, err
		}
		if String(binding["target_id"]) != target {
			return nil, ErrConflict
		}
		if binding["state"] == "linked" && Int(binding["target_generation"]) == generation {
			return binding, nil
		}
		if binding["state"] != "unlinked" {
			return nil, Invalid{"Die ursprüngliche Dokumentverknüpfung benötigt eine gesonderte Prüfung"}
		}
		err = tx.QueryRow(ctx, `UPDATE fuel.integration_bindings AS b SET state='linked',target_generation=$3,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(b)`, id.HouseholdID, binding["id"], generation).Scan(&raw)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'fuel',$2,$3,'documents','documents',$4,$5) ON CONFLICT DO NOTHING`, id.HouseholdID, source, rid, target, relation); err != nil {
			return nil, err
		}
		return decode(raw)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return fuelModuleCoreSaveBinding(ctx, tx, id, source, rid, 0, "documents", "documents", target, relation, String(data["vehicle_id"]), generation)
}

func (s *Store) fuelModuleAuthorizeCoreResult(ctx context.Context, tx pgx.Tx, id auth.Identity, result FuelModuleResult, dependencies map[string]int64) error {
	bindingID := String(result.Data["binding_id"])
	if bindingID == "" {
		return nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT to_jsonb(b) FROM fuel.integration_bindings b WHERE household_id=$1 AND id=$2`, id.HouseholdID, bindingID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	binding, err := decode(raw)
	if err != nil {
		return err
	}
	mid := String(binding["target_module"])
	if binding["state"] != "linked" || Int(binding["target_generation"]) != dependencies[mid] {
		return ErrNotFound
	}
	if historical := String(result.Data["expense_id"]); historical != "" && (mid != "expenses" || historical != String(binding["target_id"])) {
		return ErrNotFound
	}
	if err = fuelModuleCoreAuthorizeTarget(ctx, tx, id, mid, String(binding["target_entity"]), String(binding["target_id"]), dependencies[mid], true); err != nil {
		return err
	}
	if result.Action == "cost.correct" && value.ValidUUID(String(result.Data["corrects_id"])) {
		var exists bool
		_, exists, err = fuelModuleCoreBinding(ctx, tx, id, "vehicle_costs", String(result.Data["corrects_id"]), "expenses", "fuel_expense", 0)
		if err != nil {
			return err
		}
		if result.Data["corrected_expense"] == true && !exists {
			return ErrNotFound
		}
	}
	return nil
}

func fuelModuleCoreAttachment(ctx context.Context, tx pgx.Tx, id auth.Identity, attachmentID string) (Record, error) {
	if !value.ValidUUID(attachmentID) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',a.id,'module_id',a.module_id,'module_generation',a.module_generation,
 'resource_type',a.resource_type,'resource_id',a.resource_id,'media_type',a.media_type,'size_bytes',a.size_bytes,
 'sha256',encode(a.sha256,'hex'),'storage_key',a.storage_key,'created_at',a.created_at,
 'source_text',coalesce((SELECT CASE WHEN octet_length(c.source_text)<=65536 THEN c.source_text END FROM core.capture_jobs c WHERE c.household_id=a.household_id AND c.module_id='fuel'
 AND c.module_generation=a.module_generation AND c.source_attachment_id=a.id AND c.state='review' AND c.expires_at>transaction_timestamp()
 ORDER BY c.created_at DESC,c.id DESC LIMIT 1),''))
 FROM core.attachments a JOIN core.module_states ms ON ms.household_id=a.household_id AND ms.module_id='fuel'
 AND ms.state='enabled' AND ms.data_generation=a.module_generation JOIN fuel.vehicles v ON v.household_id=a.household_id AND v.id=a.resource_id
 WHERE a.household_id=$1 AND a.id=$2 AND a.module_id='fuel' AND a.resource_type='vehicles' AND a.state='ready'
 AND a.size_bytes<=10485760 AND a.media_type IN('image/jpeg','image/png','application/pdf')
 AND core.finance_artifact_visible($1,$3,$4,$5,$6,'attachment',a.id,'view')
 AND(SELECT s.classification FROM core.financial_artifact_scope_versions v JOIN core.financial_requirement_sets s ON s.household_id=v.household_id AND s.id=v.set_id
 WHERE v.household_id=a.household_id AND v.artifact_kind='attachment' AND v.object_id=a.id ORDER BY v.revision DESC LIMIT 1)='nonfinancial'`, id.HouseholdID, attachmentID,
		id.UserID, id.MembershipID, id.SessionID, id.AuthVersion).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func fuelModuleCoreDocuments(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicle string, limit, offset int) ([]Record, error) {
	if limit < 1 || limit > 201 || offset < 0 || offset > 10000 {
		return nil, Invalid{"Dokumentauswahl eingrenzen"}
	}
	return fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',b.id,'version',b.version,'document_id',d.id,'title',d.title,
 'category',d.category,'valid_until',d.valid_until,'state',b.state,'target_generation',b.target_generation)
 FROM fuel.integration_bindings b JOIN core.module_states ms ON ms.household_id=b.household_id AND ms.module_id='documents'
 AND ms.state='enabled' AND ms.data_generation=b.target_generation JOIN documents.documents d ON d.household_id=b.household_id AND d.id=b.target_id AND d.archived_at IS NULL
 WHERE b.household_id=$1 AND b.vehicle_id=$2 AND b.target_module='documents' AND b.target_entity='documents' AND b.state='linked'
 ORDER BY d.title,b.id LIMIT $3 OFFSET $4`, id.HouseholdID, vehicle, limit, offset)
}

func fuelModuleCoreBindingView(ctx context.Context, tx pgx.Tx, id auth.Identity, bindingID string) (Record, error) {
	if !value.ValidUUID(bindingID) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT to_jsonb(b) FROM fuel.integration_bindings b WHERE household_id=$1 AND id=$2`, id.HouseholdID, bindingID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	binding, err := decode(raw)
	if err != nil {
		return nil, err
	}
	view := Record{"id": binding["id"], "version": String(binding["version"]), "state": "target_unavailable"}
	if binding["state"] != "linked" {
		view["state"] = binding["state"]
		return view, nil
	}
	mid, entity, target := String(binding["target_module"]), String(binding["target_entity"]), String(binding["target_id"])
	err = fuelModuleCoreAuthorizeTarget(ctx, tx, id, mid, entity, target, Int(binding["target_generation"]), false)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
		return view, nil
	}
	if err != nil {
		return nil, err
	}
	view["state"] = "linked"
	view["url"] = "/m/" + mid + "/" + entity + "/" + target
	var title string
	switch mid + "/" + entity {
	case "chores/task_occurrences":
		err = tx.QueryRow(ctx, `SELECT t.title FROM chores.task_occurrences o JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id WHERE o.household_id=$1 AND o.id=$2`, id.HouseholdID, target).Scan(&title)
	case "documents/documents":
		err = tx.QueryRow(ctx, `SELECT title FROM documents.documents WHERE household_id=$1 AND id=$2`, id.HouseholdID, target).Scan(&title)
	case "expenses/entries":
		err = tx.QueryRow(ctx, `SELECT description FROM expenses.entries WHERE household_id=$1 AND id=$2`, id.HouseholdID, target).Scan(&title)
	default:
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	view["title"] = title
	return view, nil
}

func (s *Store) fuelModuleCoreReadOptions(ctx context.Context, tx pgx.Tx, id auth.Identity) (Record, error) {
	out := Record{"expense_categories": []Record{}, "assignees": []Record{}, "documents": []Record{}, "capture_available": s.FuelOCREnabled}
	queries := []struct{ name, query string }{
		{"expense_categories", `SELECT jsonb_build_object('id',c.id,'name',c.name) FROM expenses.categories c JOIN core.module_states ms ON ms.household_id=c.household_id AND ms.module_id='expenses' AND ms.state='enabled' WHERE c.household_id=$1 AND c.archived_at IS NULL ORDER BY c.name,c.id LIMIT 101`},
		{"assignees", `SELECT jsonb_build_object('id',m.id,'name',left(u.display_name,100)) FROM core.memberships m JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL WHERE m.household_id=$1 AND m.inactive_at IS NULL ORDER BY left(u.display_name,100),m.id LIMIT 101`},
		{"documents", `SELECT jsonb_build_object('id',d.id,'title',d.title) FROM documents.documents d JOIN core.module_states ms ON ms.household_id=d.household_id AND ms.module_id='documents' AND ms.state='enabled' WHERE d.household_id=$1 AND d.archived_at IS NULL ORDER BY d.title,d.id LIMIT 101`},
	}
	for _, item := range queries {
		rows, err := fuelReadRecords(ctx, tx, item.query, id.HouseholdID)
		if err != nil {
			return nil, err
		}
		out[item.name+"_limited"] = len(rows) > 100
		if len(rows) > 100 {
			rows = rows[:100]
		}
		out[item.name] = rows
	}
	return out, nil
}

func (s *Store) fuelModulePublishFact(ctx context.Context, tx pgx.Tx, id auth.Identity, entity string, record Record, generation int64) error {
	panes := map[string]string{"charging_sessions": "charging", "trips": "trips", "odometer_observations": "odometer", "vehicle_costs": "costs", "maintenance_plans": "maintenance", "maintenance_events": "maintenance"}
	pane, exists := panes[entity]
	if !exists || !value.ValidUUID(String(record["id"])) {
		return ErrNotFound
	}
	title := String(record["title"])
	if title == "" {
		title = "Fahrzeugbeleg · " + pane
	}
	if len(title) > 1000 {
		title = title[:1000]
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body)
 VALUES($1,'fuel',$2,$3,$4,$5,'') ON CONFLICT(household_id,module_id,resource_type,resource_id)
 DO UPDATE SET title=excluded.title,module_generation=excluded.module_generation,updated_at=clock_timestamp()`, id.HouseholdID, generation, entity, record["id"], title)
	if err != nil {
		return err
	}
	event := value.UUID()
	payload, err := json.Marshal(Record{"entity": entity, "id": record["id"], "vehicle_id": record["vehicle_id"], "pane": pane})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,actor_user_id,payload,dedupe_key)
 VALUES($1::uuid,$2,'fuel',$3,'record_changed',$4,$5,$1::text)`, event, id.HouseholdID, generation, id.UserID, payload)
	return err
}

func (s *Store) FuelModuleFactVehicle(ctx context.Context, id auth.Identity, entity, rid string) (string, string, error) {
	panes := map[string]string{"charging_sessions": "charging", "trips": "trips", "odometer_observations": "odometer", "vehicle_costs": "costs", "maintenance_plans": "maintenance", "maintenance_events": "maintenance"}
	pane, exists := panes[entity]
	if !exists || !value.ValidUUID(rid) {
		return "", "", ErrNotFound
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", "", err
	}
	defer rollback(tx)
	if _, err = fuelModuleScope(ctx, tx, id); err != nil {
		return "", "", err
	}
	var vehicle string
	err = tx.QueryRow(ctx, "SELECT vehicle_id FROM "+pgx.Identifier{"fuel", entity}.Sanitize()+" WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&vehicle)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return vehicle, pane, tx.Commit(ctx)
}

func (s *Store) fuelModuleDeleteResource(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, rid string, generation int64) error {
	if !slices.Contains([]string{"fillups", "charging_sessions", "trips"}, entity) || !value.ValidUUID(rid) {
		return ErrForbidden
	}
	var attachments int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM(SELECT id FROM core.attachments WHERE household_id=$1 AND module_id='fuel'
 AND resource_type=$2 AND resource_id=$3 AND state='ready' LIMIT 201) bounded`, id.HouseholdID, entity, rid).Scan(&attachments)
	if err != nil {
		return err
	}
	if attachments > 200 {
		return Invalid{"Vor dem Löschen die große Anhängemenge gesondert prüfen"}
	}
	queries := []string{
		`DELETE FROM core.search_documents WHERE household_id=$1 AND module_id='fuel' AND resource_type=$2 AND resource_id=$3`,
		`UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp() WHERE household_id=$1 AND module_id='fuel' AND resource_type=$2 AND resource_id=$3 AND state='pending'`,
		`UPDATE core.attachments SET state='orphaned' WHERE household_id=$1 AND module_id='fuel' AND resource_type=$2 AND resource_id=$3 AND state='ready'`,
		`INSERT INTO core.file_deletions(storage_key,household_id,attachment_id,available_at) SELECT storage_key,household_id,id,clock_timestamp()+interval '7 days'
 FROM core.attachments WHERE household_id=$1 AND module_id='fuel' AND resource_type=$2 AND resource_id=$3 AND state='orphaned' ON CONFLICT DO NOTHING`,
		`INSERT INTO core.file_deletions(storage_key,household_id,attachment_id,available_at) SELECT d.storage_key,a.household_id,a.id,clock_timestamp()+interval '7 days'
 FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id WHERE a.household_id=$1 AND a.module_id='fuel'
 AND a.resource_type=$2 AND a.resource_id=$3 AND a.state='orphaned' ON CONFLICT DO NOTHING`,
		`DELETE FROM core.resource_links WHERE household_id=$1 AND((source_module='fuel' AND source_type=$2 AND source_id=$3)
 OR(target_module='fuel' AND target_type=$2 AND target_id=$3))`,
	}
	for _, query := range queries {
		if _, err = tx.Exec(ctx, query, id.HouseholdID, entity, rid); err != nil {
			return err
		}
	}
	event := value.UUID()
	payload, err := json.Marshal(Record{"entity": entity, "id": rid})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,actor_user_id,payload,dedupe_key)
 VALUES($1::uuid,$2,'fuel',$3,'record_deleted',$4,$5,$1::text)`, event, id.HouseholdID, generation, id.UserID, payload)
	return err
}
