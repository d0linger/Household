package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func fuelModuleNormalizeCoreCommand(action string, input map[string]json.RawMessage) (Record, []FuelModuleWarning, error) {
	schemas := map[string][]string{
		"cost.save":               {"occurred_on", "kind", "title", "total_minor", "currency_code", "notes", "link_expense", "expense_category_id"},
		"cost.correct":            {"occurred_on", "kind", "title", "total_minor", "currency_code", "notes", "link_expense", "correct_expense", "expense_category_id"},
		"cost.link_expense":       {"source_entity", "expense_category_id"},
		"maintenance.save":        {"title", "notes", "due_on", "due_odometer", "odometer_series_id", "repeat_days", "repeat_distance", "active", "task_opt_in", "assignee_id"},
		"maintenance.complete":    {"completed_on", "odometer", "odometer_series_id", "notes", "next_due_on", "next_due_odometer"},
		"maintenance.link_task":   {"assignee_id"},
		"vehicle.link_document":   {"document_id"},
		"vehicle.unlink_document": {"binding_id", "binding_version"},
		"segments.rebuild":        {},
	}
	allowed, ok := schemas[action]
	if ok {
		allowed = append(allowed, "acknowledged_warnings")
	}
	if !ok || len(input) > len(allowed) {
		return nil, nil, Invalid{"Nicht unterstützter Fahrzeugvorgang"}
	}
	out := Record{}
	for name, raw := range input {
		if !slices.Contains(allowed, name) {
			return nil, nil, Invalid{"Nicht unterstütztes Feld im Fahrzeugvorgang"}
		}
		if name == "acknowledged_warnings" {
			codes := []string{}
			if len(raw) > 2048 || json.Unmarshal(raw, &codes) != nil || len(codes) > 20 {
				return nil, nil, Invalid{"Bestätigte Hinweise sind ungültig"}
			}
			for _, code := range codes {
				if len(code) > 80 || strings.TrimSpace(code) != code {
					return nil, nil, Invalid{"Bestätigter Hinweis ist ungültig"}
				}
			}
			slices.Sort(codes)
			out[name] = slices.Compact(codes)
			continue
		}
		if name == "link_expense" || name == "correct_expense" || name == "active" || name == "task_opt_in" {
			var flag bool
			if json.Unmarshal(raw, &flag) != nil || string(raw) == "null" {
				return nil, nil, Invalid{"Schalter ausdrücklich auswählen"}
			}
			out[name] = flag
			continue
		}
		limit := 200
		if name == "notes" {
			limit = 10000
		}
		if name == "binding_version" {
			var version int64
			if json.Unmarshal(raw, &version) != nil || version <= 0 {
				return nil, nil, Invalid{"Version der Verknüpfung erneut laden"}
			}
			out[name] = version
			continue
		}
		text, err := fuelModuleRawText(raw, limit)
		if err != nil {
			return nil, nil, err
		}
		if strings.HasSuffix(name, "_id") && text != "" && !value.ValidUUID(text) {
			return nil, nil, Invalid{"Referenz erneut auswählen"}
		}
		if strings.HasSuffix(name, "_id") {
			text = strings.ToLower(text)
		}
		if strings.HasSuffix(name, "_on") && text != "" && !value.Date(text) {
			return nil, nil, Invalid{"Gültiges Kalenderdatum angeben"}
		}
		if slices.Contains([]string{"due_odometer", "odometer", "next_due_odometer", "repeat_distance"}, name) && text != "" {
			if _, err = fuelModuleDecimal(text, name == "repeat_distance"); err != nil {
				return nil, nil, err
			}
		}
		if name == "repeat_days" && text != "" {
			days, err := strconv.Atoi(text)
			if err != nil || days < 1 || days > 36500 {
				return nil, nil, Invalid{"Wiederholung zwischen 1 und 36500 Tagen wählen"}
			}
		}
		out[name] = text
	}
	require := func(names ...string) error {
		for _, name := range names {
			if String(out[name]) == "" {
				return Invalid{"Pflichtfeld fehlt: " + name}
			}
		}
		return nil
	}
	switch action {
	case "cost.save", "cost.correct":
		if err := require("occurred_on", "kind", "title", "total_minor", "currency_code"); err != nil {
			return nil, nil, err
		}
		if !slices.Contains([]string{"insurance", "tax", "repair", "maintenance", "parking", "toll", "other"}, String(out["kind"])) || !fuelCurrencyPattern.MatchString(String(out["currency_code"])) {
			return nil, nil, Invalid{"Kostenart und Währung ausdrücklich auswählen"}
		}
		if _, err := fuelModuleMinor(String(out["total_minor"]), true); err != nil {
			return nil, nil, err
		}
		if out["link_expense"] == true && !value.ValidUUID(String(out["expense_category_id"])) {
			return nil, nil, Invalid{"Ausgabenkategorie auswählen"}
		}
		if out["correct_expense"] == true && out["link_expense"] != true {
			return nil, nil, Invalid{"Beim Stornieren die neue Ausgabe ebenfalls ausdrücklich bestätigen"}
		}
	case "cost.link_expense":
		if err := require("source_entity", "expense_category_id"); err != nil {
			return nil, nil, err
		}
		if !slices.Contains([]string{"vehicle_costs", "fillups", "charging_sessions"}, String(out["source_entity"])) {
			return nil, nil, Invalid{"Unbekannte Kostenquelle"}
		}
	case "maintenance.save":
		if err := require("title"); err != nil {
			return nil, nil, err
		}
		if String(out["due_on"]) == "" && String(out["due_odometer"]) == "" || String(out["due_odometer"]) != "" && String(out["odometer_series_id"]) == "" {
			return nil, nil, Invalid{"Datum oder Kilometerziel mit eindeutiger Zählerreihe angeben"}
		}
		if _, ok := out["active"]; !ok {
			out["active"] = true
		}
		if _, ok := out["task_opt_in"]; !ok {
			out["task_opt_in"] = false
		}
	case "maintenance.complete":
		if err := require("completed_on"); err != nil {
			return nil, nil, err
		}
		if (String(out["odometer"]) == "") != (String(out["odometer_series_id"]) == "") {
			return nil, nil, Invalid{"Kilometerstand zusammen mit seiner Zählerreihe angeben"}
		}
	case "vehicle.link_document":
		if err := require("document_id"); err != nil {
			return nil, nil, err
		}
	case "vehicle.unlink_document":
		if err := require("binding_id"); err != nil {
			return nil, nil, err
		}
		if Int(out["binding_version"]) <= 0 {
			return nil, nil, Invalid{"Verknüpfung erneut laden"}
		}
	}
	return out, []FuelModuleWarning{}, nil
}

func fuelModuleCoreDependencies(command FuelModuleCommand, data Record) ([]string, error) {
	dependencies := []string{}
	switch command.Action {
	case "cost.link_expense":
		dependencies = append(dependencies, "expenses")
	case "cost.save", "cost.correct":
		if data["link_expense"] == true {
			dependencies = append(dependencies, "expenses")
		}
	case "maintenance.link_task":
		dependencies = append(dependencies, "chores")
	case "vehicle.link_document", "vehicle.unlink_document":
		dependencies = append(dependencies, "documents")
	}
	if len(command.TargetGenerations) != len(dependencies) {
		return nil, Invalid{"Nur die tatsächlich benötigten Modulversionen bestätigen"}
	}
	for _, dependency := range dependencies {
		if command.TargetGenerations[dependency] <= 0 {
			return nil, Invalid{"Zielmodul erneut laden und prüfen"}
		}
	}
	return dependencies, nil
}

func fuelModuleCoreRecord(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, rid, vehicle string, lock bool) (Record, error) {
	if !slices.Contains([]string{"vehicles", "odometer_series", "fillups", "charging_sessions", "vehicle_costs", "maintenance_plans", "integration_bindings"}, entity) || !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	fields := map[string][]string{"vehicles": {"name", "registration", "notes"}, "odometer_series": {"reason"}, "fillups": {"station", "notes"},
		"charging_sessions": {"location", "notes"}, "vehicle_costs": {"title", "notes"}, "maintenance_plans": {"title", "notes"}, "integration_bindings": {"relation", "target_entity"}}
	terms := []string{}
	for _, field := range fields[entity] {
		terms = append(terms, "coalesce(octet_length(t."+pgx.Identifier{field}.Sanitize()+"),0)::bigint")
	}
	query := "SELECT CASE WHEN " + strings.Join(terms, "+") + "<=131072 THEN to_jsonb(t) END FROM " + pgx.Identifier{"fuel", entity}.Sanitize() + " t WHERE household_id=$1 AND id=$2"
	// Costs are append-only for the runtime role. The operation serializes Fuel
	// changes through household_state; a row lock must not require UPDATE rights
	// on immutable facts. BeginMutation also prevents concurrent owner purging.
	if lock && entity != "vehicle_costs" {
		query += " FOR UPDATE NOWAIT"
	}
	var raw []byte
	err := tx.QueryRow(ctx, query, id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 256<<10 {
		return nil, Invalid{"Gespeicherter Fahrzeugdatensatz überschreitet die unterstützte Größe"}
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	owner := String(record["vehicle_id"])
	if entity == "vehicles" {
		owner = String(record["id"])
	}
	if owner != vehicle {
		return nil, ErrNotFound
	}
	return record, nil
}

func (s *Store) fuelModulePreviewCoreCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (FuelModulePreview, error) {
	out := FuelModulePreview{Command: command, After: data, Effects: []Record{}, Warnings: []FuelModuleWarning{}}
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Context = scope
	effect := Record{"module": "fuel", "vehicle_id": command.VehicleID}
	switch command.Action {
	case "cost.save":
		effect["action"] = "Neue Fahrzeugkosten dokumentieren"
	case "cost.correct":
		effect["action"] = "Originalkosten erhalten und eine ausdrückliche Korrektur dokumentieren"
		effect["source_id"] = command.RecordID
	case "maintenance.save":
		effect["action"] = "Wartungsplan mit ausdrücklich gewählten Fälligkeiten speichern"
	case "maintenance.complete":
		effect["action"] = "Aktuellen Wartungszyklus einmalig abschließen und bestätigte nächste Ziele übernehmen"
		effect["source_id"] = command.RecordID
	case "segments.rebuild":
		effect["action"] = "Begrenzte Segment-Neuberechnung für dieses Fahrzeug einreihen"
		effect["chunk_limit"] = 500
		effect["business_facts_changed"] = false
	}
	if effect["action"] != nil {
		out.Effects = append(out.Effects, effect)
	}
	if !scope.CanWrite || command.Generation != scope.Generation {
		return out, ErrForbidden
	}
	if _, err = fuelModuleCoreRecord(ctx, tx, id, "vehicles", command.VehicleID, command.VehicleID, false); err != nil {
		return out, err
	}
	deps, err := fuelModuleCoreDependencies(command, data)
	if err != nil {
		return out, err
	}
	for _, dependency := range deps {
		current, err := requireModule(ctx, tx, id, dependency)
		if err != nil || current != command.TargetGenerations[dependency] {
			if err != nil {
				return out, err
			}
			return out, ErrConflict
		}
		out.Effects = append(out.Effects, Record{"module": dependency, "action": "explizite Verknüpfung", "generation": current})
	}
	entity := ""
	switch command.Action {
	case "cost.correct":
		entity = "vehicle_costs"
	case "cost.link_expense":
		entity = String(data["source_entity"])
	case "maintenance.save", "maintenance.complete", "maintenance.link_task":
		entity = "maintenance_plans"
	case "vehicle.unlink_document":
		entity = "integration_bindings"
		command.RecordID = String(data["binding_id"])
	}
	if command.RecordID != "" && entity != "" {
		out.Before, err = fuelModuleCoreRecord(ctx, tx, id, entity, command.RecordID, command.VehicleID, false)
		if err != nil {
			return out, err
		}
		version := command.Version
		if command.Action == "vehicle.unlink_document" {
			version = Int(data["binding_version"])
		}
		if Int(out.Before["version"]) != version {
			return out, ErrConflict
		}
		out.Before = fuelModuleWireRecord(out.Before)
		if command.Action == "vehicle.unlink_document" {
			if out.Before["target_module"] != "documents" || out.Before["target_entity"] != "documents" || Int(out.Before["target_generation"]) != command.TargetGenerations["documents"] {
				return out, ErrNotFound
			}
			if err = fuelModuleCoreAuthorizeTarget(ctx, tx, id, "documents", "documents", String(out.Before["target_id"]), command.TargetGenerations["documents"], false); err != nil {
				return FuelModulePreview{}, err
			}
			out.Before = Record{"id": out.Before["id"], "version": out.Before["version"], "vehicle_id": out.Before["vehicle_id"], "state": out.Before["state"]}
		}
	}
	if command.Action == "cost.save" && command.RecordID != "" || slices.Contains([]string{"cost.correct", "cost.link_expense", "maintenance.complete", "maintenance.link_task"}, command.Action) && command.RecordID == "" {
		return out, Invalid{"Bestehenden Datensatz mit seiner Version auswählen; Kosten durch Korrektur ergänzen"}
	}
	if String(data["odometer_series_id"]) != "" {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM fuel.odometer_series WHERE household_id=$1 AND vehicle_id=$2 AND id=$3)`, id.HouseholdID, command.VehicleID, data["odometer_series_id"]).Scan(&exists); err != nil || !exists {
			if err != nil {
				return out, err
			}
			return out, ErrNotFound
		}
	}
	if command.Action == "cost.correct" {
		if data["correct_expense"] == true {
			if _, err = s.fuelModuleCoreCorrectExpense(ctx, tx, id, command.RecordID, command.TargetGenerations["expenses"], false); err != nil {
				return out, err
			}
			out.Effects = append(out.Effects, Record{"module": "expenses", "action": "Verknüpfte gebuchte Ausgabe stornieren und durch neue Ausgabe ersetzen"})
		} else if err = fuelModuleAssertUnlinked(ctx, tx, id, "vehicle_costs", command.RecordID); err != nil {
			return out, Invalid{"Die verknüpfte Ausgabe muss bei der Kostenkorrektur ausdrücklich storniert und ersetzt werden"}
		}
	}
	if command.Action == "maintenance.complete" && String(data["completed_on"]) > scope.Today {
		return out, Invalid{"Wartung erst nach tatsächlicher Durchführung abschließen"}
	}
	if command.Action == "maintenance.complete" && String(data["odometer_series_id"]) != "" {
		if err = fuelModuleCoreValidateReading(ctx, tx, id, command.VehicleID, String(data["odometer_series_id"]), String(data["completed_on"]), String(data["odometer"])); err != nil {
			return out, err
		}
	}
	if command.Action == "vehicle.link_document" {
		if err = fuelModuleCoreAuthorizeTarget(ctx, tx, id, "documents", "documents", String(data["document_id"]), command.TargetGenerations["documents"], false); err != nil {
			return out, err
		}
	}
	out.Command.SourceStamp = scope.Stamp
	return out, nil
}

func fuelModuleCoreValidateReading(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicle, series, day, reading string) error {
	var initial string
	var final, ended *string
	var started string
	err := tx.QueryRow(ctx, `SELECT initial_reading::text,final_reading::text,started_on::text,ended_on::text FROM fuel.odometer_series
 WHERE household_id=$1 AND vehicle_id=$2 AND id=$3`, id.HouseholdID, vehicle, series).Scan(&initial, &final, &started, &ended)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	observed, err := fuelModuleDecimal(reading, false)
	if err != nil {
		return err
	}
	start, err := fuelModuleDecimal(initial, false)
	if err != nil {
		return err
	}
	if day < started || ended != nil && day > *ended || observed.Cmp(start) < 0 {
		return Invalid{"Wartungsstand liegt außerhalb der bestätigten Zählerreihe"}
	}
	if final != nil {
		finish, err := fuelModuleDecimal(*final, false)
		if err != nil {
			return err
		}
		if observed.Cmp(finish) > 0 {
			return Invalid{"Wartungsstand überschreitet den tatsächlichen Abschlussstand"}
		}
	}
	return nil
}

func nullableFuel(data Record, name string) any {
	if data[name] == nil || String(data[name]) == "" {
		return nil
	}
	return data[name]
}

func (s *Store) fuelModuleApplyCoreCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, operationID string) (Record, error) {
	preview, err := s.fuelModulePreviewCoreCommand(ctx, tx, id, command, data)
	if err != nil {
		return nil, err
	}
	if command.SourceStamp != preview.Context.Stamp {
		return nil, ErrConflict
	}
	rid := command.RecordID
	var raw []byte
	switch command.Action {
	case "cost.save", "cost.correct":
		var corrects any
		if command.Action == "cost.correct" {
			if _, err = fuelModuleCoreRecord(ctx, tx, id, "vehicle_costs", rid, command.VehicleID, true); err != nil {
				return nil, err
			}
			corrects = rid
		}
		rid = value.UUID()
		err = tx.QueryRow(ctx, `INSERT INTO fuel.vehicle_costs AS t(id,household_id,vehicle_id,occurred_on,kind,title,total_minor,currency_code,notes,corrects_id,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING to_jsonb(t)`, rid, id.HouseholdID, command.VehicleID, data["occurred_on"], data["kind"], data["title"], data["total_minor"], data["currency_code"], String(data["notes"]), corrects, id.UserID).Scan(&raw)
		if err != nil {
			return nil, err
		}
		result, err := decode(raw)
		if err != nil {
			return nil, err
		}
		result = fuelModuleWireRecord(result)
		if err = s.fuelModulePublishFact(ctx, tx, id, "vehicle_costs", result, command.Generation); err != nil {
			return nil, err
		}
		if data["link_expense"] == true {
			result["expense_category_id"] = data["expense_category_id"]
			if data["correct_expense"] == true {
				original, err := s.fuelModuleCoreCorrectExpense(ctx, tx, id, command.RecordID, command.TargetGenerations["expenses"], true)
				if err != nil {
					return nil, err
				}
				result["replaces_entry_id"] = original
			}
			binding, err := s.fuelModuleCoreExpense(ctx, tx, id, "vehicle_costs", rid, result, command.TargetGenerations["expenses"])
			if err != nil {
				return nil, err
			}
			result["binding_id"] = binding["id"]
			if data["correct_expense"] == true {
				result["corrected_expense"] = true
			}
			delete(result, "replaces_entry_id")
			delete(result, "expense_category_id")
		}
		return result, nil
	case "cost.link_expense":
		source := String(data["source_entity"])
		fact, err := fuelModuleCoreRecord(ctx, tx, id, source, rid, command.VehicleID, true)
		if err != nil {
			return nil, err
		}
		fact["expense_category_id"] = data["expense_category_id"]
		binding, err := s.fuelModuleCoreExpense(ctx, tx, id, source, rid, fact, command.TargetGenerations["expenses"])
		if err != nil {
			return nil, err
		}
		result := Record{"id": rid, "version": fact["version"], "binding_id": binding["id"]}
		if len(command.legacyHash) == 32 {
			result["expense_id"] = binding["target_id"]
		}
		return result, nil
	case "maintenance.save":
		if err = fuelModuleCoreAssignee(ctx, tx, id, String(data["assignee_id"])); err != nil {
			return nil, err
		}
		if rid == "" {
			rid = value.UUID()
			err = tx.QueryRow(ctx, `INSERT INTO fuel.maintenance_plans AS t(id,household_id,vehicle_id,title,notes,due_on,due_odometer,odometer_series_id,repeat_days,repeat_distance,active,task_opt_in,assignee_id,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING to_jsonb(t)`, rid, id.HouseholdID, command.VehicleID, data["title"], String(data["notes"]), nullableFuel(data, "due_on"), nullableFuel(data, "due_odometer"), nullableFuel(data, "odometer_series_id"), nullableFuel(data, "repeat_days"), nullableFuel(data, "repeat_distance"), data["active"], data["task_opt_in"], nullableFuel(data, "assignee_id"), id.UserID).Scan(&raw)
		} else {
			err = tx.QueryRow(ctx, `UPDATE fuel.maintenance_plans AS t SET title=$4,notes=$5,due_on=$6,due_odometer=$7,odometer_series_id=$8,repeat_days=$9,repeat_distance=$10,active=$11,task_opt_in=$12,assignee_id=$13,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND id=$3 AND version=$14 RETURNING to_jsonb(t)`, id.HouseholdID, command.VehicleID, rid, data["title"], String(data["notes"]), nullableFuel(data, "due_on"), nullableFuel(data, "due_odometer"), nullableFuel(data, "odometer_series_id"), nullableFuel(data, "repeat_days"), nullableFuel(data, "repeat_distance"), data["active"], data["task_opt_in"], nullableFuel(data, "assignee_id"), command.Version).Scan(&raw)
		}
	case "maintenance.complete":
		plan, err := fuelModuleCoreRecord(ctx, tx, id, "maintenance_plans", rid, command.VehicleID, true)
		if err != nil {
			return nil, err
		}
		if plan["active"] != true {
			return nil, Invalid{"Inaktiven Wartungsplan zuerst prüfen"}
		}
		event := value.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO fuel.maintenance_events(id,household_id,vehicle_id,plan_id,cycle,completed_on,odometer,odometer_series_id,notes,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, event, id.HouseholdID, command.VehicleID, rid, plan["cycle"], data["completed_on"], nullableFuel(data, "odometer"), nullableFuel(data, "odometer_series_id"), String(data["notes"]), id.UserID)
		if err != nil {
			return nil, err
		}
		continuing := String(data["next_due_on"]) != "" || String(data["next_due_odometer"]) != ""
		if String(data["next_due_odometer"]) != "" && String(data["odometer_series_id"]) == "" {
			return nil, Invalid{"Nächstes Kilometerziel benötigt die ausdrücklich bestätigte Zählerreihe"}
		}
		err = tx.QueryRow(ctx, `UPDATE fuel.maintenance_plans AS t SET cycle=cycle+1,active=$4,
 due_on=CASE WHEN $4 THEN $5::date ELSE due_on END,due_odometer=CASE WHEN $4 THEN $6::numeric ELSE due_odometer END,
 odometer_series_id=CASE WHEN $6::numeric IS NOT NULL THEN $7::uuid ELSE odometer_series_id END,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND id=$3 AND version=$8 RETURNING to_jsonb(t)`, id.HouseholdID, command.VehicleID, rid, continuing, nullableFuel(data, "next_due_on"), nullableFuel(data, "next_due_odometer"), nullableFuel(data, "odometer_series_id"), command.Version).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, err
		}
	case "maintenance.link_task":
		plan, err := fuelModuleCoreRecord(ctx, tx, id, "maintenance_plans", rid, command.VehicleID, true)
		if err != nil {
			return nil, err
		}
		if assignee, supplied := data["assignee_id"]; supplied {
			plan["assignee_id"] = assignee
		}
		binding, err := s.fuelModuleCoreTask(ctx, tx, id, "maintenance_plans", rid, plan, command.TargetGenerations["chores"])
		if err != nil {
			return nil, err
		}
		return Record{"id": rid, "version": plan["version"], "binding_id": binding["id"]}, nil
	case "vehicle.link_document":
		binding, err := s.fuelModuleCoreDocument(ctx, tx, id, "vehicles", command.VehicleID, Record{"document_id": data["document_id"], "vehicle_id": command.VehicleID}, command.TargetGenerations["documents"])
		if err != nil {
			return nil, err
		}
		return Record{"id": command.VehicleID, "binding_id": binding["id"]}, nil
	case "vehicle.unlink_document":
		binding, err := fuelModuleCoreRecord(ctx, tx, id, "integration_bindings", String(data["binding_id"]), command.VehicleID, true)
		if err != nil {
			return nil, err
		}
		if binding["target_module"] != "documents" || binding["state"] != "linked" || Int(binding["version"]) != Int(data["binding_version"]) {
			return nil, ErrConflict
		}
		if _, err = tx.Exec(ctx, `DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='fuel' AND source_type=$2 AND source_id=$3 AND target_module='documents' AND target_type='documents' AND target_id=$4 AND relation=$5`, id.HouseholdID, binding["source_entity"], binding["source_id"], binding["target_id"], binding["relation"]); err != nil {
			return nil, err
		}
		err = tx.QueryRow(ctx, `UPDATE fuel.integration_bindings AS t SET state='unlinked',version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING to_jsonb(t)`, id.HouseholdID, binding["id"], data["binding_version"]).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, err
		}
	case "segments.rebuild":
		_, err = tx.Exec(ctx, `INSERT INTO fuel.segment_state(household_id,vehicle_id,source_version) VALUES($1,$2,1) ON CONFLICT(household_id,vehicle_id) DO UPDATE SET source_version=fuel.segment_state.source_version+1,status='pending',updated_at=clock_timestamp()`, id.HouseholdID, command.VehicleID)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO fuel.segment_jobs(household_id,vehicle_id,target_version) SELECT household_id,vehicle_id,source_version FROM fuel.segment_state WHERE household_id=$1 AND vehicle_id=$2 ON CONFLICT(household_id,vehicle_id) DO UPDATE SET target_version=excluded.target_version,state='pending',attempts=0,next_attempt_at=clock_timestamp(),lease_until=NULL`, id.HouseholdID, command.VehicleID)
		return Record{"id": command.VehicleID, "queued": err == nil}, err
	default:
		return nil, ErrNotFound
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	record = fuelModuleWireRecord(record)
	if command.Action == "vehicle.unlink_document" {
		record = Record{"id": record["id"], "version": record["version"], "vehicle_id": record["vehicle_id"], "state": record["state"]}
	}
	if command.Action == "maintenance.save" || command.Action == "maintenance.complete" {
		if err = s.fuelModulePublishFact(ctx, tx, id, "maintenance_plans", record, command.Generation); err != nil {
			return nil, err
		}
	}
	return record, nil
}

func fuelModuleAssertUnlinked(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, rid string) error {
	var linked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM fuel.integration_bindings WHERE household_id=$1 AND source_entity=$2 AND source_id=$3 AND target_module='expenses')
 OR EXISTS(SELECT 1 FROM core.resource_links WHERE household_id=$1 AND source_module='fuel' AND source_type=$2 AND source_id=$3 AND target_module='expenses')`, id.HouseholdID, entity, rid).Scan(&linked)
	if err != nil {
		return err
	}
	if linked {
		return Invalid{"Mit einer Ausgabe verknüpfte Originalwerte bleiben erhalten. Die verknüpfte Ausgabe im Finanzmodul prüfen; dieser Fahrzeugbeleg kann nicht geändert oder gelöscht werden"}
	}
	return nil
}
