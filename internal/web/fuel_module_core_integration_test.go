//go:build integration

package web

import (
	"errors"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func fuelModuleCoreNativeEnable(t *testing.T, f *integrationFixture, module string) {
	t.Helper()
	var state string
	var version int64
	if err := f.owner.QueryRow(f.ctx, `SELECT state,state_version FROM core.module_states WHERE household_id=$1 AND module_id=$2`,
		f.identity.HouseholdID, module).Scan(&state, &version); err != nil {
		t.Fatal("actual optional module state", module, err)
	}
	if state != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, module, true, version, value.UUID()); err != nil {
			t.Fatal("normal administrator module enable", module, err)
		}
	}
	if current, enabled := f.app.Store.State(module); !enabled || current.State != "enabled" {
		t.Fatal("administrator module enable did not refresh application state", module)
	}
}

func fuelModuleCoreNativeGeneration(t *testing.T, f *integrationFixture, module string) int64 {
	t.Helper()
	var generation int64
	if err := f.owner.QueryRow(f.ctx, `SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id=$2 AND state='enabled'`,
		f.identity.HouseholdID, module).Scan(&generation); err != nil || generation <= 0 {
		t.Fatal("actual enabled integration generation", module, generation, err)
	}
	return generation
}

// Snapshot the small, isolated fixture's facts, durable receipts and affected
// projections. A rejected integration must leave the whole transaction absent.
func fuelModuleCoreNativeSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var digest string
	err := f.owner.QueryRow(f.ctx, `SELECT encode(sha256(convert_to(jsonb_build_object(
 'costs',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM fuel.vehicle_costs t WHERE household_id=$1),
 'plans',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM fuel.maintenance_plans t WHERE household_id=$1),
 'events',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM fuel.maintenance_events t WHERE household_id=$1),
 'bindings',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM fuel.integration_bindings t WHERE household_id=$1),
 'revision',(SELECT coalesce(jsonb_agg(to_jsonb(t)),'[]') FROM fuel.household_state t WHERE household_id=$1),
 'segments',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY vehicle_id),'[]') FROM fuel.segment_state t WHERE household_id=$1),
 'operations',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM fuel.module_operations t WHERE household_id=$1),
 'effects',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY operation_id,ordinal),'[]') FROM fuel.module_effects t
 WHERE operation_id IN(SELECT id FROM fuel.module_operations WHERE household_id=$1)),
 'expenses',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM expenses.entries t WHERE household_id=$1),
 'lines',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM expenses.entry_lines t WHERE household_id=$1),
 'templates',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM chores.task_templates t WHERE household_id=$1),
 'tasks',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM chores.task_occurrences t WHERE household_id=$1),
 'documents',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM documents.documents t WHERE household_id=$1),
 'links',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM core.resource_links t WHERE household_id=$1),
 'search',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY module_id,resource_type,resource_id),'[]') FROM core.search_documents t WHERE household_id=$1),
 'outbox',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM core.outbox_events t WHERE household_id=$1),
 'reminders',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM core.reminders t WHERE household_id=$1),
 'audit',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM core.audit_log t WHERE household_id=$1)
 )::text,'UTF8')),'hex')`, f.identity.HouseholdID).Scan(&digest)
	if err != nil {
		t.Fatal("complete integration rollback snapshot", err)
	}
	return digest
}

func TestFuelModuleCoreCostsCorrectExpenseAtomicallyAndKeepDurableDedup(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, _ := fuelModuleNativeVehicle(t, f)
	vid := store.String(vehicle["id"])
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "SYNTHETIC vehicle cost category"})
	gen := fuelModuleCoreNativeGeneration(t, f, "expenses")
	targets := map[string]int64{"expenses": gen}
	today := fuelModuleReadNativeDay(t, f).Format(time.DateOnly)
	original, originalCommand, originalKey := fuelModuleNativeApply(t, f, "cost.save", vid, "", 0, store.Record{
		"occurred_on": today, "kind": "repair", "title": "SYNTHETIC original repair", "total_minor": "1000", "currency_code": "EUR",
		"notes": "original immutable evidence", "link_expense": true, "expense_category_id": category["id"],
	}, targets)
	var originalExpense, originalBinding string
	if err := f.owner.QueryRow(f.ctx, `SELECT id,target_id FROM fuel.integration_bindings
 WHERE household_id=$1 AND source_entity='vehicle_costs' AND source_id=$2 AND relation='fuel_expense'`,
		f.identity.HouseholdID, original.RecordID).Scan(&originalBinding, &originalExpense); err != nil || originalBinding != store.String(original.Data["binding_id"]) {
		t.Fatal("actual durable expense binding missing", err)
	}
	var amount int64
	var state, kind string
	if err := f.owner.QueryRow(f.ctx, `SELECT amount_minor,state,kind FROM expenses.entries WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, originalExpense).Scan(&amount, &state, &kind); err != nil || amount != 1000 || state != "posted" || kind != "expense" {
		t.Fatal("expense was not posted with exact source amount", amount, state, kind, err)
	}
	if _, err := f.owner.Exec(f.ctx, `DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='fuel'
 AND source_type='vehicle_costs' AND source_id=$2 AND target_module='expenses'`, f.identity.HouseholdID, original.RecordID); err != nil {
		t.Fatal(err)
	}
	linked, _, _ := fuelModuleNativeApply(t, f, "cost.link_expense", vid, original.RecordID, original.Version,
		store.Record{"source_entity": "vehicle_costs", "expense_category_id": category["id"]}, targets)
	if store.String(linked.Data["binding_id"]) != originalBinding {
		t.Fatal("new key recreated a financial effect after Core link pruning")
	}
	var expenseCount int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM expenses.entries WHERE household_id=$1`, f.identity.HouseholdID).Scan(&expenseCount); err != nil || expenseCount != 1 {
		t.Fatal("durable new-key reuse duplicated the expense", expenseCount, err)
	}
	replayed, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, originalKey, value.UUID(), originalCommand)
	if err != nil || replayed.OperationID != original.OperationID {
		t.Fatal("current-authority accepted cost receipt could not replay", err)
	}
	uncorrected := fuelModuleNativeCommand(t, f, "cost.correct", vid, original.RecordID, original.Version, store.Record{
		"occurred_on": today, "kind": "repair", "title": "unconfirmed correction", "total_minor": "1200", "currency_code": "EUR",
	}, nil)
	before := fuelModuleCoreNativeSnapshot(t, f)
	if _, err = f.app.Store.FuelModulePreview(f.ctx, f.identity, uncorrected); err == nil {
		t.Fatal("linked cost correction omitted its explicit financial correction")
	}
	if fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("denied correction preview changed facts or projections")
	}
	corrected, correctedCommand, correctedKey := fuelModuleNativeApply(t, f, "cost.correct", vid, original.RecordID, original.Version, store.Record{
		"occurred_on": today, "kind": "repair", "title": "SYNTHETIC corrected repair", "total_minor": "1200", "currency_code": "EUR",
		"notes": "explicit correction evidence", "link_expense": true, "correct_expense": true, "expense_category_id": category["id"],
	}, targets)
	if corrected.RecordID == original.RecordID || store.String(corrected.Data["corrects_id"]) != original.RecordID || corrected.Data["corrected_expense"] != true {
		t.Fatal("correction overwrote its original identity or lost explicit financial intent")
	}
	var costs, reversals, replacements, lines int
	var net string
	if err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM fuel.vehicle_costs WHERE household_id=$1),
 (SELECT count(*) FROM expenses.entries WHERE household_id=$1 AND reverses_entry_id=$2 AND amount_minor=-1000 AND state='posted'),
 (SELECT count(*) FROM expenses.entries WHERE household_id=$1 AND replaces_entry_id=$2 AND amount_minor=1200 AND state='posted'),
 (SELECT sum(amount_minor)::text FROM expenses.entries WHERE household_id=$1),
 (SELECT count(*) FROM expenses.entry_lines WHERE household_id=$1)`, f.identity.HouseholdID, originalExpense).
		Scan(&costs, &reversals, &replacements, &net, &lines); err != nil || costs != 2 || reversals != 1 || replacements != 1 || net != "1200" || lines != 3 {
		t.Fatal("correction did not commit one balanced financial replacement", costs, reversals, replacements, net, lines, err)
	}
	var originalAmount, originalVersion int64
	var originalNotes string
	if err = f.owner.QueryRow(f.ctx, `SELECT total_minor,version,notes FROM fuel.vehicle_costs WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, original.RecordID).Scan(&originalAmount, &originalVersion, &originalNotes); err != nil || originalAmount != 1000 || originalVersion != 1 || originalNotes != "original immutable evidence" {
		t.Fatal("original cost facts were mutated", err)
	}
	view := fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "costs", VehicleID: vid, From: today, To: today})
	groups := fuelModuleReadNativeRows(t, view.Totals["groups"])
	if len(view.Records) != 1 || store.String(view.Records[0]["id"]) != corrected.RecordID || len(groups) != 1 ||
		store.String(groups[0]["amount_minor"]) != "1200" || groups[0]["cost_per_km"] != nil {
		t.Fatal("effective cost view double-counted old facts, ledger entries or undocumented distance", view.Totals)
	}
	before = fuelModuleCoreNativeSnapshot(t, f)
	replayed, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, correctedKey, value.UUID(), correctedCommand)
	if err != nil || replayed.OperationID != corrected.OperationID || fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("correction replay duplicated effects or changed projections", err)
	}
	foreign := fuelModuleNativeReview(t, f, fuelModuleNativeCommand(t, f, "cost.save", vid, "", 0, store.Record{
		"occurred_on": today, "kind": "repair", "title": "SYNTHETIC USD must not auto-convert", "total_minor": "900719925", "currency_code": "USD",
		"link_expense": true, "expense_category_id": category["id"],
	}, targets))
	before = fuelModuleCoreNativeSnapshot(t, f)
	if _, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, value.UUID(), value.UUID(), foreign.Command); err == nil {
		t.Fatal("foreign currency silently became a household-currency expense")
	}
	if fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("late currency refusal left a cost, receipt, binding, audit or projection")
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='expenses'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	before = fuelModuleCoreNativeSnapshot(t, f)
	if _, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, correctedKey, value.UUID(), correctedCommand); err == nil || fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("accepted financial receipt bypassed current dependency availability", err)
	}
}

func TestFuelModuleCoreMaintenanceTaskDedupCyclesDSTAndDocumentedKilometres(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelModuleCoreNativeEnable(t, f, "chores")
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.households SET timezone='Europe/Vienna' WHERE id=$1`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	gen := fuelModuleCoreNativeGeneration(t, f, "chores")
	targets := map[string]int64{"chores": gen}
	plan, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", vid, "", 0, store.Record{
		"title": "SYNTHETIC DST service", "due_on": "2027-03-28", "repeat_days": "30", "active": true,
		"task_opt_in": true, "assignee_id": f.identity.MembershipID,
	}, nil)
	linked, _, _ := fuelModuleNativeApply(t, f, "maintenance.link_task", vid, plan.RecordID, plan.Version,
		store.Record{"assignee_id": f.identity.MembershipID}, targets)
	bindingID := store.String(linked.Data["binding_id"])
	var taskID, templateID, timezone, occurrenceKey string
	var due time.Time
	if err := f.owner.QueryRow(f.ctx, `SELECT o.id,o.template_id,o.due_at,o.occurrence_key,t.timezone
 FROM fuel.integration_bindings b JOIN chores.task_occurrences o ON o.household_id=b.household_id AND o.id=b.target_id
 JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id WHERE b.household_id=$1 AND b.id=$2`,
		f.identity.HouseholdID, bindingID).Scan(&taskID, &templateID, &due, &occurrenceKey, &timezone); err != nil ||
		!due.Equal(time.Date(2027, 3, 28, 7, 0, 0, 0, time.UTC)) || timezone != "Europe/Vienna" || occurrenceKey != "fuel/"+plan.RecordID+"/1" {
		t.Fatal("household civil day lost DST-aware 09:00 scheduling", due, timezone, occurrenceKey, err)
	}
	if _, err := f.owner.Exec(f.ctx, `DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='fuel'
 AND source_id=$2 AND target_module='chores'`, f.identity.HouseholdID, plan.RecordID); err != nil {
		t.Fatal(err)
	}
	again, _, _ := fuelModuleNativeApply(t, f, "maintenance.link_task", vid, plan.RecordID, plan.Version, store.Record{}, targets)
	if store.String(again.Data["binding_id"]) != bindingID {
		t.Fatal("current cycle lost its durable task identity after Core pruning")
	}
	var tasks, templates int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM chores.task_occurrences WHERE household_id=$1),
 (SELECT count(*) FROM chores.task_templates WHERE household_id=$1)`, f.identity.HouseholdID).Scan(&tasks, &templates); err != nil || tasks != 1 || templates != 1 {
		t.Fatal("same cycle/new key duplicated a task", tasks, templates, err)
	}
	today := fuelModuleReadNativeDay(t, f)
	completed, completionCommand, completionKey := fuelModuleNativeApply(t, f, "maintenance.complete", vid, plan.RecordID, plan.Version, store.Record{
		"completed_on": today.Format(time.DateOnly), "notes": "actual explicit completion", "next_due_on": "2027-10-31",
	}, nil)
	if store.Int(completed.Data["cycle"]) != 2 || completed.Data["active"] != true {
		t.Fatal("explicit next cycle was not retained")
	}
	second, _, _ := fuelModuleNativeApply(t, f, "maintenance.link_task", vid, plan.RecordID, completed.Version, store.Record{}, targets)
	if store.String(second.Data["binding_id"]) == bindingID {
		t.Fatal("new maintenance cycle reused a prior cycle's task")
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT o.due_at FROM fuel.integration_bindings b JOIN chores.task_occurrences o
 ON o.household_id=b.household_id AND o.id=b.target_id WHERE b.household_id=$1 AND b.id=$2`, f.identity.HouseholdID, second.Data["binding_id"]).Scan(&due); err != nil ||
		!due.Equal(time.Date(2027, 10, 31, 8, 0, 0, 0, time.UTC)) {
		t.Fatal("winter DST boundary shifted the household task time", due, err)
	}
	before := fuelModuleCoreNativeSnapshot(t, f)
	replayed, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, completionKey, value.UUID(), completionCommand)
	if err != nil || replayed.OperationID != completed.OperationID || fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("completion replay advanced a cycle twice", err)
	}
	_, _, _ = fuelModuleNativeApply(t, f, "observation.save", vid, "", 0, store.Record{
		"odometer_series_id": sid, "observed_on": today.Format(time.DateOnly), "reading": "120", "notes": "actual measured threshold",
	}, nil)
	_, _, _ = fuelModuleNativeApply(t, f, "observation.save", vid, "", 0, store.Record{
		"odometer_series_id": sid, "observed_on": today.AddDate(0, 0, 1).Format(time.DateOnly), "reading": "10000", "notes": "future cannot make a task due now",
	}, nil)
	kmPlan, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", vid, "", 0, store.Record{
		"title": "SYNTHETIC first reached date or kilometres", "due_on": today.AddDate(0, 0, 30).Format(time.DateOnly), "due_odometer": "100",
		"odometer_series_id": sid, "active": true, "task_opt_in": true,
	}, nil)
	kmLinked, _, _ := fuelModuleNativeApply(t, f, "maintenance.link_task", vid, kmPlan.RecordID, kmPlan.Version, store.Record{}, targets)
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	wantDue := time.Date(today.Year(), today.Month(), today.Day(), 9, 0, 0, 0, location)
	if err = f.owner.QueryRow(f.ctx, `SELECT o.due_at FROM fuel.integration_bindings b JOIN chores.task_occurrences o
 ON o.household_id=b.household_id AND o.id=b.target_id WHERE b.household_id=$1 AND b.id=$2`, f.identity.HouseholdID, kmLinked.Data["binding_id"]).Scan(&due); err != nil || !due.Equal(wantDue) {
		t.Fatal("reached kilometres failed the earlier household-day rule", due, wantDue, err)
	}
	notDue, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", vid, "", 0, store.Record{
		"title": "SYNTHETIC future reading is insufficient", "due_odometer": "500", "odometer_series_id": sid, "active": true, "task_opt_in": true,
	}, nil)
	preview := fuelModuleNativeReview(t, f, fuelModuleNativeCommand(t, f, "maintenance.link_task", vid, notDue.RecordID, notDue.Version, store.Record{}, targets))
	before = fuelModuleCoreNativeSnapshot(t, f)
	if _, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, value.UUID(), value.UUID(), preview.Command); err == nil {
		t.Fatal("future odometer point silently generated a present task")
	}
	if fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("unreached kilometre refusal left task/template/receipt effects")
	}
}

func TestFuelModuleCoreDocumentBindingsRelinkAndRespectCurrentTargetAuthority(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelModuleCoreNativeEnable(t, f, "documents")
	vehicle, _ := fuelModuleNativeVehicle(t, f)
	vid := store.String(vehicle["id"])
	doc1, _ := auditCreate(t, f, "documents", "documents", store.Record{"title": "SYNTHETIC vehicle policy", "category": "insurance"})
	doc2, _ := auditCreate(t, f, "documents", "documents", store.Record{"title": "SYNTHETIC vehicle warranty", "category": "warranty"})
	gen := fuelModuleCoreNativeGeneration(t, f, "documents")
	targets := map[string]int64{"documents": gen}
	first, firstCommand, firstKey := fuelModuleNativeApply(t, f, "vehicle.link_document", vid, vid, store.Int(vehicle["version"]), store.Record{"document_id": doc1["id"]}, targets)
	second, _, _ := fuelModuleNativeApply(t, f, "vehicle.link_document", vid, vid, store.Int(vehicle["version"]), store.Record{"document_id": doc2["id"]}, targets)
	bindingID := store.String(first.Data["binding_id"])
	if bindingID == "" || bindingID == store.String(second.Data["binding_id"]) {
		t.Fatal("independent vehicle documents collided on one relation")
	}
	view := fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "documents", VehicleID: vid})
	if len(view.Records) != 2 {
		t.Fatal("current documents view lost an independently bound document")
	}
	if _, err := f.owner.Exec(f.ctx, `DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='fuel'
 AND source_id=$2 AND target_module='documents'`, f.identity.HouseholdID, vid); err != nil {
		t.Fatal(err)
	}
	again, _, _ := fuelModuleNativeApply(t, f, "vehicle.link_document", vid, vid, store.Int(vehicle["version"]), store.Record{"document_id": doc1["id"]}, targets)
	if store.String(again.Data["binding_id"]) != bindingID {
		t.Fatal("pruned Core link caused another document binding")
	}
	var bindingVersion int64
	if err := f.owner.QueryRow(f.ctx, `SELECT version FROM fuel.integration_bindings WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, bindingID).Scan(&bindingVersion); err != nil {
		t.Fatal(err)
	}
	_, _, _ = fuelModuleNativeApply(t, f, "vehicle.unlink_document", vid, bindingID, bindingVersion,
		store.Record{"binding_id": bindingID, "binding_version": bindingVersion}, targets)
	view = fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "documents", VehicleID: vid})
	if len(view.Records) != 1 || store.String(view.Records[0]["document_id"]) != store.String(doc2["id"]) {
		t.Fatal("unlink removed the document or another independent relation")
	}
	relinked, _, _ := fuelModuleNativeApply(t, f, "vehicle.link_document", vid, vid, store.Int(vehicle["version"]), store.Record{"document_id": doc1["id"]}, targets)
	if store.String(relinked.Data["binding_id"]) != bindingID {
		t.Fatal("explicit relink replaced the durable document identity")
	}
	var bindings, documents int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM fuel.integration_bindings WHERE household_id=$1 AND target_module='documents'),
 (SELECT count(*) FROM documents.documents WHERE household_id=$1)`, f.identity.HouseholdID).Scan(&bindings, &documents); err != nil || bindings != 2 || documents != 2 {
		t.Fatal("link lifecycle duplicated/deleted documents or bindings", bindings, documents, err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE documents.documents SET archived_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, doc1["id"]); err != nil {
		t.Fatal(err)
	}
	view = fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "documents", VehicleID: vid})
	if len(view.Records) != 1 || store.String(view.Records[0]["document_id"]) != store.String(doc2["id"]) {
		t.Fatal("archived document leaked through a durable binding")
	}
	before := fuelModuleCoreNativeSnapshot(t, f)
	if _, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, firstKey, value.UUID(), firstCommand); !errors.Is(err, store.ErrNotFound) || fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("accepted document receipt bypassed current archive/target authority", err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.module_states SET data_generation=data_generation+1 WHERE household_id=$1 AND module_id='documents'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	view = fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "documents", VehicleID: vid})
	if len(view.Records) != 0 {
		t.Fatal("old-generation durable binding exposed current-generation document facts")
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1`, f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.FuelModuleWorkspace(f.ctx, f.identity, store.FuelModuleQuery{Pane: "documents", VehicleID: vid}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("documents workspace trusted a stale actor", err)
	}
	before = fuelModuleCoreNativeSnapshot(t, f)
	if _, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, firstKey, value.UUID(), firstCommand); !errors.Is(err, store.ErrForbidden) || fuelModuleCoreNativeSnapshot(t, f) != before {
		t.Fatal("document replay trusted a revoked actor or changed facts", err)
	}
}
