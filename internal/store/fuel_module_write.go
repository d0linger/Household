package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fuelImportDecision struct {
	RowNumber    string `json:"row_number"`
	Fingerprint  string `json:"fingerprint"`
	Decision     string `json:"decision"`
	DuplicateAck bool   `json:"duplicate_ack"`
	SeriesID     string `json:"odometer_series_id,omitempty"`
}

func fuelModuleIsCoreAction(action string) bool {
	return slices.Contains([]string{"cost.save", "cost.correct", "cost.link_expense", "maintenance.save", "maintenance.complete", "maintenance.link_task", "vehicle.link_document", "vehicle.unlink_document", "segments.rebuild"}, action)
}

func fuelModuleNormalizeCommand(command FuelModuleCommand) (Record, []FuelModuleWarning, error) {
	if fuelModuleIsCoreAction(command.Action) {
		return fuelModuleNormalizeCoreCommand(command.Action, command.Data)
	}
	return fuelModuleNormalizeInput(command.Action, command.Data)
}

func fuelModuleError(err error) error {
	if problem, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch problem.Code {
		case "42501":
			return ErrForbidden
		case "P0002":
			return ErrNotFound
		case "40001", "55P03", "40P01", "23505":
			return ErrConflict
		case "22023":
			return Invalid{"Angaben erneut prüfen und bestätigen"}
		}
	}
	return err
}

func fuelModuleDecodeResponse(raw []byte, output any) error {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return ErrConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return ErrConflict
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrConflict
	}
	return nil
}

func fuelModuleCheckDependencies(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, normalized Record) error {
	dependencies, err := fuelModuleCoreDependencies(command, normalized)
	if err != nil {
		return err
	}
	for _, module := range dependencies {
		generation, generationErr := requireModule(ctx, tx, id, module)
		if generationErr != nil {
			return generationErr
		}
		if command.TargetGenerations[module] != generation {
			return ErrConflict
		}
	}
	return nil
}

func (s *Store) FuelModulePreview(ctx context.Context, id auth.Identity, command FuelModuleCommand) (FuelModulePreview, error) {
	command.VehicleID, command.RecordID = strings.ToLower(command.VehicleID), strings.ToLower(command.RecordID)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "fuel", command.Generation)
	if err != nil {
		return FuelModulePreview{}, fuelModuleError(err)
	}
	defer rollback(tx)
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return FuelModulePreview{}, err
	}
	if command.SourceStamp != "" && command.SourceStamp != scope.Stamp {
		return FuelModulePreview{}, ErrConflict
	}
	command.SourceStamp = scope.Stamp
	if err = fuelModuleCommandIdentity(command); err != nil {
		return FuelModulePreview{}, err
	}
	normalized, warnings, err := fuelModuleNormalizeCommand(command)
	if err != nil {
		return FuelModulePreview{}, err
	}
	if err = fuelModuleCheckDependencies(ctx, tx, id, command, normalized); err != nil {
		return FuelModulePreview{}, fuelModuleError(err)
	}
	preview, err := s.fuelModulePlanCommand(ctx, tx, id, command, normalized)
	if err != nil {
		return FuelModulePreview{}, fuelModuleError(err)
	}
	preview.Context = scope
	preview.Before = fuelModuleWireRecord(preview.Before)
	preview.After = fuelModuleWireRecord(preview.After)
	preview.Warnings = append(warnings, preview.Warnings...)
	command.Data, err = fuelModuleRawValues(normalized)
	if err != nil {
		return FuelModulePreview{}, err
	}
	preview.Command = command
	if preview.Effects == nil {
		preview.Effects = []Record{}
	}
	if preview.Warnings == nil {
		preview.Warnings = []FuelModuleWarning{}
	}
	return preview, nil
}

func (s *Store) FuelModuleExecute(ctx context.Context, id auth.Identity, key, requestID string, command FuelModuleCommand) (FuelModuleResult, error) {
	command.VehicleID, command.RecordID = strings.ToLower(command.VehicleID), strings.ToLower(command.RecordID)
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !value.ValidUUID(key) {
		return FuelModuleResult{}, Invalid{"Stabilen Vorgangsschlüssel verwenden"}
	}
	if err := fuelModuleCommandIdentity(command); err != nil {
		return FuelModuleResult{}, err
	}
	normalized, warnings, err := fuelModuleNormalizeCommand(command)
	if err != nil {
		return FuelModuleResult{}, err
	}
	command.Data, err = fuelModuleRawValues(normalized)
	if err != nil {
		return FuelModuleResult{}, err
	}
	hash, err := fuelModuleCommandDigest(command)
	if err != nil {
		return FuelModuleResult{}, err
	}
	tx, err := s.BeginMutation(ctx, id, "fuel", command.Generation)
	if err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	defer rollback(tx)
	if _, err = fuelModuleScope(ctx, tx, id); err != nil {
		return FuelModuleResult{}, err
	}
	if err = fuelModuleCheckDependencies(ctx, tx, id, command, normalized); err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	dependencies := command.TargetGenerations
	if dependencies == nil {
		dependencies = map[string]int64{}
	}
	dependencyJSON, err := json.Marshal(dependencies)
	if err != nil {
		return FuelModuleResult{}, err
	}
	operationID := value.UUID()
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT fuel.module_begin($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)",
		operationID, strings.ToLower(key), command.Generation, command.Action, hash, dependencyJSON,
		id.SessionID, id.AuthVersion, id.MembershipID).Scan(&raw)
	if err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	if len(raw) > 256<<10 {
		return FuelModuleResult{}, ErrConflict
	}
	var reservation struct {
		Replay       *bool            `json:"replay"`
		OperationID  string           `json:"operation_id"`
		Result       FuelModuleResult `json:"result"`
		Dependencies map[string]int64 `json:"dependencies"`
	}
	if fuelModuleDecodeResponse(raw, &reservation) != nil || reservation.Replay == nil {
		return FuelModuleResult{}, ErrConflict
	}
	if *reservation.Replay {
		if reservation.Result.Action != command.Action || !value.ValidUUID(reservation.Result.OperationID) {
			return FuelModuleResult{}, ErrConflict
		}
		if len(reservation.Dependencies) != len(dependencies) {
			return FuelModuleResult{}, ErrConflict
		}
		for module, generation := range dependencies {
			if reservation.Dependencies[module] != generation {
				return FuelModuleResult{}, ErrConflict
			}
		}
		if err = s.fuelModuleAuthorizeCoreResult(ctx, tx, id, reservation.Result, reservation.Dependencies); err != nil {
			return FuelModuleResult{}, err
		}
		reservation.Result.Data = fuelModuleWireRecord(reservation.Result.Data)
		return reservation.Result, nil
	}
	if reservation.OperationID != operationID {
		return FuelModuleResult{}, ErrConflict
	}
	// module_begin holds the household revision fence in its fresh branch. This
	// fresh READ COMMITTED snapshot closes the gap after waiting for that fence.
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return FuelModuleResult{}, err
	}
	if scope.Stamp != command.SourceStamp {
		return FuelModuleResult{}, ErrConflict
	}
	preview, err := s.fuelModulePlanCommand(ctx, tx, id, command, normalized)
	if err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	warnings = append(warnings, preview.Warnings...)
	if command.Action != "import.stage" && command.Action != "capture.stage" {
		if err = fuelModuleAcknowledge(warnings, normalized); err != nil {
			return FuelModuleResult{}, err
		}
	}
	var record Record
	if fuelModuleIsCoreAction(command.Action) {
		record, err = s.fuelModuleApplyCoreCommand(ctx, tx, id, command, normalized, operationID)
	} else {
		record, err = s.fuelModuleApplyCommand(ctx, tx, id, command, normalized, requestID)
	}
	if err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	var acceptedAt time.Time
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&acceptedAt); err != nil {
		return FuelModuleResult{}, err
	}
	result := FuelModuleResult{OperationID: operationID, Action: command.Action, AcceptedAt: acceptedAt.UTC().Format(time.RFC3339Nano),
		RecordID: String(record["id"]), Version: Int(record["version"]), Message: "Fahrzeugvorgang übernommen", Data: fuelModuleWireRecord(record)}
	resultJSON, err := json.Marshal(result)
	if err != nil || len(resultJSON) > 256<<10 {
		return FuelModuleResult{}, Invalid{"Ergebnis ist zu groß; Auswahl eingrenzen"}
	}
	if err = tx.QueryRow(ctx, "SELECT fuel.module_finish($1,$2::jsonb)", operationID, resultJSON).Scan(&raw); err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	if fuelModuleDecodeResponse(raw, &result) != nil || result.OperationID != operationID || result.Action != command.Action {
		return FuelModuleResult{}, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return FuelModuleResult{}, fuelModuleError(err)
	}
	return result, nil
}

func (s *Store) FuelModuleOperationStatus(ctx context.Context, id auth.Identity, key string, generation int64) (Record, error) {
	if !value.ValidUUID(key) {
		return nil, Invalid{"Stabilen Vorgangsschlüssel verwenden"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "fuel", generation)
	if err != nil {
		return nil, fuelModuleError(err)
	}
	defer rollback(tx)
	if _, err = fuelModuleScope(ctx, tx, id); err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT fuel.module_status($1,$2,$3,$4,$5)", strings.ToLower(key), generation,
		id.SessionID, id.AuthVersion, id.MembershipID).Scan(&raw)
	if err != nil {
		return nil, fuelModuleError(err)
	}
	return s.fuelModuleOperationReply(ctx, tx, id, raw)
}

func (s *Store) FuelModuleAbandonOperation(ctx context.Context, id auth.Identity, key string, generation int64, requestID string) (Record, error) {
	if !value.ValidUUID(key) {
		return nil, Invalid{"Stabilen Vorgangsschlüssel verwenden"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "fuel", generation)
	if err != nil {
		return nil, fuelModuleError(err)
	}
	defer rollback(tx)
	if _, err = fuelModuleScope(ctx, tx, id); err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT fuel.module_abandon($1,$2,$3,$4,$5)", strings.ToLower(key), generation,
		id.SessionID, id.AuthVersion, id.MembershipID).Scan(&raw); err != nil {
		return nil, fuelModuleError(err)
	}
	reply, err := s.fuelModuleOperationReply(ctx, tx, id, raw)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fuelModuleError(err)
	}
	return reply, nil
}

func (s *Store) fuelModuleOperationReply(ctx context.Context, tx pgx.Tx, id auth.Identity, raw []byte) (Record, error) {
	if len(raw) > 256<<10 {
		return nil, ErrConflict
	}
	var status struct {
		State        string           `json:"state"`
		Message      string           `json:"message"`
		Result       FuelModuleResult `json:"result"`
		Dependencies map[string]int64 `json:"dependencies"`
	}
	if fuelModuleDecodeResponse(raw, &status) != nil {
		return nil, ErrConflict
	}
	if (status.State == "complete" || status.State == "refused") && value.ValidUUID(status.Result.OperationID) {
		if status.State == "refused" && status.Result.Action != "operation.cancel" {
			return nil, ErrConflict
		}
		if err := s.fuelModuleAuthorizeCoreResult(ctx, tx, id, status.Result, status.Dependencies); err != nil {
			return nil, err
		}
		status.Result.Data = fuelModuleWireRecord(status.Result.Data)
		return Record{"state": status.State, "result": status.Result}, nil
	}
	if status.State != "unknown" {
		return nil, ErrConflict
	}
	return Record{"state": "unknown", "message": "Ergebnis nicht sicher verfügbar; denselben Vorgangsschlüssel beibehalten und erneut prüfen"}, nil
}

func fuelModuleCommandIdentity(command FuelModuleCommand) error {
	if command.Generation <= 0 || command.Version < 0 || len(command.Action) > 80 {
		return Invalid{"Vorgang oder Modulversion ist ungültig"}
	}
	if len(command.legacyHash) != 0 && len(command.legacyHash) != sha256.Size {
		return Invalid{"Interne Vorgangsidentität ist ungültig"}
	}
	for _, reference := range []string{command.VehicleID, command.RecordID} {
		if reference != "" && !value.ValidUUID(reference) {
			return Invalid{"Fahrzeug oder Datensatz ist ungültig"}
		}
	}
	stamp, err := hex.DecodeString(command.SourceStamp)
	if err != nil || len(stamp) != sha256.Size || command.SourceStamp != strings.ToLower(command.SourceStamp) {
		return Invalid{"Aktuellen Fahrzeugstand laden und erneut prüfen"}
	}
	if len(command.TargetGenerations) > 3 {
		return Invalid{"Nicht unterstützte Modulabhängigkeiten"}
	}
	for module, generation := range command.TargetGenerations {
		if !slices.Contains([]string{"expenses", "chores", "documents"}, module) || generation <= 0 {
			return Invalid{"Nicht unterstützte Modulabhängigkeit oder Generation"}
		}
	}
	return nil
}

func fuelModuleRawValues(values Record) (map[string]json.RawMessage, error) {
	output := make(map[string]json.RawMessage, len(values))
	for field, entry := range values {
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		output[field] = raw
	}
	return output, nil
}

func fuelModuleCommandDigest(command FuelModuleCommand) ([]byte, error) {
	if len(command.legacyHash) == sha256.Size {
		input := append([]byte("fuel-legacy-v1"), command.legacyHash...)
		hash := sha256.Sum256(input)
		return slices.Clone(hash[:]), nil
	}
	encoded, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 13<<20 {
		return nil, Invalid{"Geprüfter Fahrzeugvorgang überschreitet die zulässige Größe"}
	}
	hash := sha256.Sum256(encoded)
	return slices.Clone(hash[:]), nil
}

func fuelModuleAcknowledge(warnings []FuelModuleWarning, data Record) error {
	codes := []string{}
	if data["acknowledged_warnings"] != nil {
		if err := fuelModuleImportObject(data["acknowledged_warnings"], &codes); err != nil {
			return err
		}
	}
	for _, warning := range warnings {
		if warning.Code != "" && !slices.Contains(codes, warning.Code) {
			return Invalid{"Alle aktuellen Hinweise vor der Übernahme ausdrücklich bestätigen"}
		}
	}
	return nil
}

func fuelModuleImportDecisions(raw any) ([]fuelImportDecision, error) {
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > 64<<10 {
		return nil, Invalid{"Importentscheidungen sind ungültig oder zu groß"}
	}
	decisions := []fuelImportDecision{}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decisions) != nil || len(decisions) == 0 || len(decisions) > 100 {
		return nil, Invalid{"Eine bis hundert Zeilen pro Übernahme ausdrücklich auswählen"}
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, Invalid{"Importentscheidungen enthalten weitere Daten"}
	}
	seen := map[string]bool{}
	for _, decision := range decisions {
		position, positionErr := strconv.Atoi(decision.RowNumber)
		if positionErr != nil || position < 1 || position > fuelImportMaxRows || strconv.Itoa(position) != decision.RowNumber || seen[decision.RowNumber] {
			return nil, Invalid{"Importzeile ist ungültig oder mehrfach ausgewählt"}
		}
		seen[decision.RowNumber] = true
		if decision.SeriesID != "" && !value.ValidUUID(decision.SeriesID) {
			return nil, Invalid{"Ausdrücklich ausgewählte Zählerreihe ist ungültig"}
		}
		if decision.Decision != "import" && decision.Decision != "skip" {
			return nil, Invalid{"Jede Importzeile ausdrücklich übernehmen oder überspringen"}
		}
		fingerprint, fingerprintErr := hex.DecodeString(decision.Fingerprint)
		if fingerprintErr != nil || len(fingerprint) != sha256.Size || decision.Fingerprint != strings.ToLower(decision.Fingerprint) {
			return nil, Invalid{"Zeileninhalt muss erneut geladen und geprüft werden"}
		}
	}
	return decisions, nil
}

func fuelModuleEntity(action string) string {
	switch action {
	case "vehicle.save", "vehicle.select_series", "vehicle.archive":
		return "vehicles"
	case "series.save", "odometer.replace":
		return "odometer_series"
	case "fillup.save", "fillup.delete":
		return "fillups"
	case "charge.save", "charge.delete":
		return "charging_sessions"
	case "trip.save", "trip.delete":
		return "trips"
	case "observation.save":
		return "odometer_observations"
	}
	return ""
}

func (s *Store) fuelModuleProjectFact(ctx context.Context, tx pgx.Tx, id auth.Identity, entity string, record Record, generation int64) error {
	if record["deleted"] == true {
		return s.fuelModuleDeleteResource(ctx, tx, id, entity, String(record["id"]), generation)
	}
	if descriptor, ok := s.Module("fuel"); ok {
		if compiled, exists := descriptor.Entity(entity); exists {
			return s.afterWrite(ctx, tx, id, descriptor, compiled, record, generation)
		}
	}
	return s.fuelModulePublishFact(ctx, tx, id, entity, record, generation)
}

func fuelModuleFact(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, recordID, vehicleID string) (Record, error) {
	fields := map[string][]string{
		"vehicles":              {"name", "registration", "fuel_type", "notes"},
		"odometer_series":       {"reason"},
		"fillups":               {"fuel_type", "quantity_unit", "currency_code", "station", "notes", "source_kind"},
		"charging_sessions":     {"measurement_basis", "currency_code", "location", "charge_kind", "notes"},
		"trips":                 {"purpose", "notes"},
		"odometer_observations": {"source_kind", "notes"},
	}
	textFields, ok := fields[entity]
	if !ok || !value.ValidUUID(recordID) {
		return nil, ErrNotFound
	}
	terms := make([]string, 0, len(textFields))
	for _, field := range textFields {
		terms = append(terms, "coalesce(octet_length(t."+pgx.Identifier{field}.Sanitize()+"),0)::bigint")
	}
	query := "SELECT CASE WHEN " + strings.Join(terms, "+") + "<=131072 THEN to_jsonb(t) END FROM " + pgx.Identifier{"fuel", entity}.Sanitize() + " t WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT"
	var raw []byte
	err := tx.QueryRow(ctx, query, id.HouseholdID, recordID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 256<<10 {
		return nil, Invalid{"Gespeicherter Datensatz überschreitet die zulässige Größe"}
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	owner := String(record["vehicle_id"])
	if entity == "vehicles" {
		owner = String(record["id"])
	}
	if vehicleID != "" && owner != vehicleID {
		return nil, ErrNotFound
	}
	return fuelModuleWireRecord(record), nil
}

func (s *Store) fuelModulePlanCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (FuelModulePreview, error) {
	if fuelModuleIsCoreAction(command.Action) {
		return s.fuelModulePreviewCoreCommand(ctx, tx, id, command, data)
	}
	preview := FuelModulePreview{Command: command, Before: Record{}, After: Record{}, Effects: []Record{}, Warnings: []FuelModuleWarning{}}
	scope, scopeErr := fuelModuleScope(ctx, tx, id)
	if scopeErr != nil {
		return preview, scopeErr
	}
	for field, entry := range data {
		preview.After[field] = entry
	}
	if command.Action == "settings.save" {
		settings, err := fuelModuleSettings(ctx, tx, id, command.Generation)
		if err != nil {
			return preview, err
		}
		if Int(settings["version"]) != command.Version {
			return preview, ErrConflict
		}
		_, vehicleSupplied := data["default_vehicle_id"]
		_, seriesSupplied := data["default_series_id"]
		for _, field := range fuelModulePreferenceKeys() {
			if _, supplied := data[field]; !supplied {
				data[field] = settings[field]
			}
		}
		raw, rawErr := fuelModuleRawValues(data)
		if rawErr != nil {
			return preview, rawErr
		}
		canonical, _, canonicalErr := fuelModuleNormalizeInput("settings.save", raw)
		if canonicalErr != nil {
			return preview, canonicalErr
		}
		for field, entry := range canonical {
			data[field] = entry
		}
		preview.After = canonical
		if vehicle := String(data["default_vehicle_id"]); vehicle != "" {
			selectedVehicle, vehicleErr := fuelModuleFact(ctx, tx, id, "vehicles", vehicle, vehicle)
			if vehicleErr != nil {
				return preview, vehicleErr
			}
			if vehicleSupplied && vehicle != String(settings["default_vehicle_id"]) && String(selectedVehicle["archived_at"]) != "" {
				return preview, Invalid{"Neues Standardfahrzeug muss aktiv sein"}
			}
			if seriesID := String(data["default_series_id"]); seriesID != "" {
				series, seriesErr := fuelModuleFact(ctx, tx, id, "odometer_series", seriesID, vehicle)
				active := String(series["started_on"]) <= scope.Today && (String(series["ended_on"]) == "" || String(series["ended_on"]) >= scope.Today)
				if seriesErr != nil || (seriesSupplied && seriesID != String(settings["default_series_id"]) && !active) {
					return preview, Invalid{"Persönliche Zählerreihe muss zum aktiven Fahrzeug gehören und offen sein"}
				}
			}
		}
		preview.Before = settings
		preview.Effects = append(preview.Effects, Record{"kind": "personal_settings", "scope": "current_user"})
		return preview, nil
	}
	if command.Action == "vehicle.save" && command.RecordID == "" {
		if command.Version != 0 || command.VehicleID != "" {
			return preview, Invalid{"Neues Fahrzeug ohne vorhandene Datensatzkennung anlegen"}
		}
		preview.Effects = append(preview.Effects, Record{"kind": "create", "entity": "vehicles"})
		return preview, nil
	}
	if !value.ValidUUID(command.VehicleID) {
		return preview, Invalid{"Fahrzeug ausdrücklich auswählen"}
	}
	vehicle, err := fuelModuleFact(ctx, tx, id, "vehicles", command.VehicleID, command.VehicleID)
	if err != nil {
		return preview, err
	}
	if String(vehicle["archived_at"]) != "" && command.Action != "vehicle.archive" {
		return preview, Invalid{"Archiviertes Fahrzeug kann keine neuen Vorgänge annehmen"}
	}
	entity := fuelModuleEntity(command.Action)
	if command.RecordID != "" && entity != "" {
		current, currentErr := fuelModuleFact(ctx, tx, id, entity, command.RecordID, command.VehicleID)
		if currentErr != nil {
			return preview, currentErr
		}
		if command.Version <= 0 || Int(current["version"]) != command.Version {
			return preview, ErrConflict
		}
		preview.Before = current
		preview.After = Record{}
		for field, entry := range current {
			preview.After[field] = entry
		}
		for field, entry := range data {
			preview.After[field] = entry
		}
		preview.After["version"] = strconv.FormatInt(command.Version+1, 10)
		if entity == "fillups" || entity == "charging_sessions" {
			if err = fuelModuleAssertUnlinked(ctx, tx, id, entity, command.RecordID); err != nil {
				return preview, err
			}
		}
	} else if command.Version != 0 && entity != "" {
		return preview, Invalid{"Neue Datensätze ohne alte Version anlegen"}
	}
	switch command.Action {
	case "vehicle.archive":
		if command.RecordID != command.VehicleID || (String(vehicle["archived_at"]) != "") == (data["archived"] == true) {
			return preview, Invalid{"Aktuellen Archivzustand laden; Fahrzeug befindet sich bereits im gewünschten Zustand"}
		}
		delete(preview.After, "archived_at")
		preview.Effects = append(preview.Effects, Record{"kind": "vehicle_archive", "id": command.VehicleID, "archived": data["archived"]})
	case "fillup.save", "charge.save", "trip.save", "observation.save":
		if err = s.fuelModulePlanPhysical(ctx, tx, id, command, data, vehicle, &preview); err != nil {
			return preview, err
		}
	case "series.save":
		if err = fuelModuleSeriesFactsFit(ctx, tx, id, command.VehicleID, command.RecordID, data); err != nil {
			return preview, err
		}
	case "vehicle.select_series":
		series, seriesErr := fuelModuleFact(ctx, tx, id, "odometer_series", String(data["odometer_series_id"]), command.VehicleID)
		if seriesErr != nil {
			return preview, seriesErr
		}
		active := String(series["started_on"]) <= scope.Today && (String(series["ended_on"]) == "" || String(series["ended_on"]) >= scope.Today)
		if command.RecordID != command.VehicleID || Int(series["version"]) != Int(data["series_version"]) || !active {
			return preview, ErrConflict
		}
		preview.After["current_series_id"] = data["odometer_series_id"]
		delete(preview.After, "odometer_series_id")
		delete(preview.After, "series_version")
	case "odometer.replace":
		if command.RecordID == "" || String(vehicle["current_series_id"]) != command.RecordID || String(preview.Before["ended_on"]) != "" {
			return preview, Invalid{"Aktuell gewählte offene Zählerreihe vor dem Wechsel ausdrücklich auswählen"}
		}
		closing := Record{"started_on": preview.Before["started_on"], "initial_reading": preview.Before["initial_reading"], "ended_on": data["closed_on"], "final_reading": data["final_reading"]}
		if err = fuelModuleSeriesFactsFit(ctx, tx, id, command.VehicleID, command.RecordID, closing); err != nil {
			return preview, err
		}
		preview.Effects = append(preview.Effects, Record{"kind": "close_series", "id": command.RecordID}, Record{"kind": "create_series", "started_on": data["started_on"], "initial_reading": data["initial_reading"], "continuous_distance": "unknown_unless_previously_proved"})
	case "import.stage":
		parsed, parseWarnings, parseErr := fuelModuleImportParse(ctx, String(data["profile"]), String(data["csv_text"]), data)
		if parseErr != nil {
			return preview, parseErr
		}
		if err = s.fuelModulePlanImportRows(ctx, tx, id, command, data, parsed); err != nil {
			return preview, err
		}
		preview.After, preview.Warnings = fuelModuleImportPreviewRows(parsed), parseWarnings
		preview.Effects = append(preview.Effects, Record{"kind": "stage_import", "business_records": "0"})
	case "import.confirm":
		if err = s.fuelModulePlanImportConfirm(ctx, tx, id, command, data, &preview); err != nil {
			return preview, err
		}
	case "capture.stage", "capture.confirm":
		if err = s.fuelModulePlanCapture(ctx, tx, id, command, data, &preview); err != nil {
			return preview, err
		}
	case "fillup.delete", "charge.delete", "trip.delete":
		if command.RecordID == "" {
			return preview, Invalid{"Zu löschenden Datensatz und seine Version ausdrücklich auswählen"}
		}
	case "vehicle.save":
		if command.RecordID != command.VehicleID {
			return preview, ErrConflict
		}
	default:
		return preview, Invalid{"Nicht unterstützter Fahrzeugvorgang"}
	}
	if len(preview.Effects) == 0 {
		kind := "create"
		if command.RecordID != "" {
			kind = "update"
		}
		if strings.HasSuffix(command.Action, ".delete") {
			kind = "delete"
		}
		preview.Effects = append(preview.Effects, Record{"kind": kind, "entity": entity, "id": command.RecordID})
	}
	return preview, nil
}

const fuelModulePhysicalRows = `SELECT 'fillups'::text AS entity,id,occurred_on AS day,odometer AS reading,odometer_series_id AS series FROM fuel.fillups WHERE household_id=$1 AND vehicle_id=$2
 UNION ALL SELECT 'charging_sessions',id,occurred_on,odometer,odometer_series_id FROM fuel.charging_sessions WHERE household_id=$1 AND vehicle_id=$2 AND odometer IS NOT NULL
 UNION ALL SELECT 'odometer_observations',id,observed_on,reading,odometer_series_id FROM fuel.odometer_observations WHERE household_id=$1 AND vehicle_id=$2
 UNION ALL SELECT 'trips',id,started_on,start_odometer,odometer_series_id FROM fuel.trips WHERE household_id=$1 AND vehicle_id=$2
 UNION ALL SELECT 'trips',id,ended_on,end_odometer,odometer_series_id FROM fuel.trips WHERE household_id=$1 AND vehicle_id=$2`

func fuelModuleSeriesFactsFit(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicleID, seriesID string, data Record) error {
	initial, err := fuelModuleDecimal(String(data["initial_reading"]), false)
	if err != nil || !value.Date(String(data["started_on"])) {
		return Invalid{"Anfangsstand und Beginn der Zählerreihe prüfen"}
	}
	if final := String(data["final_reading"]); final != "" {
		closing, closingErr := fuelModuleDecimal(final, false)
		if closingErr != nil || closing.Cmp(initial) < 0 {
			return Invalid{"Abschlussstand darf nicht unter dem Anfangsstand liegen"}
		}
	}
	if end := String(data["ended_on"]); end != "" && (!value.Date(end) || end < String(data["started_on"])) {
		return Invalid{"Abschlussdatum der Zählerreihe prüfen"}
	}
	if seriesID == "" {
		return nil
	}
	var violates bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM (`+fuelModulePhysicalRows+`) facts
 WHERE series=$3 AND(day<$4::date OR day>nullif($5,'')::date OR reading<$6::numeric OR reading>nullif($7,'')::numeric))`,
		id.HouseholdID, vehicleID, seriesID, data["started_on"], String(data["ended_on"]), data["initial_reading"], String(data["final_reading"])).Scan(&violates)
	if err != nil {
		return err
	}
	if violates {
		return Invalid{"Änderung der Zählerreihe widerspricht vorhandenen Tank-, Lade-, Foto- oder Fahrtdaten"}
	}
	return nil
}

func fuelModuleHistoryBounds(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, seriesID, day, reading string) (Record, error) {
	var previous, next *string
	err := tx.QueryRow(ctx, `SELECT max(reading) FILTER(WHERE day<$4::date)::text,min(reading) FILTER(WHERE day>$4::date)::text
 FROM (`+fuelModulePhysicalRows+`) facts WHERE series=$3 AND(nullif($6,'') IS NULL OR NOT(entity=$5 AND id=nullif($6,'')::uuid))`,
		id.HouseholdID, command.VehicleID, seriesID, day, fuelModuleEntity(command.Action), command.RecordID).Scan(&previous, &next)
	if err != nil {
		return nil, err
	}
	number, err := fuelModuleDecimal(reading, false)
	if err != nil {
		return nil, err
	}
	for _, bound := range []struct {
		text  *string
		lower bool
	}{{text: previous, lower: true}, {text: next}} {
		if bound.text == nil {
			continue
		}
		limit, limitErr := fuelModuleDecimal(*bound.text, false)
		if limitErr != nil || (bound.lower && number.Cmp(limit) < 0) || (!bound.lower && number.Cmp(limit) > 0) {
			return nil, Invalid{"Zählerstand widerspricht vorherigen oder nachfolgenden bestätigten Fahrzeugdaten"}
		}
	}
	comparison := Record{"lower_bound": nil, "upper_bound": nil}
	if previous != nil {
		comparison["lower_bound"] = *previous
	}
	if next != nil {
		comparison["upper_bound"] = *next
	}
	var priorEntity, priorID, priorDay, priorReading string
	err = tx.QueryRow(ctx, `SELECT entity,id::text,day::text,reading::text FROM (`+fuelModulePhysicalRows+`) facts
 WHERE series=$3 AND(nullif($6,'') IS NULL OR NOT(entity=$5 AND id=nullif($6,'')::uuid)) AND day<=$4::date AND reading<=$7::numeric
 ORDER BY day DESC,reading DESC,id DESC LIMIT 1`, id.HouseholdID, command.VehicleID, seriesID, day,
		fuelModuleEntity(command.Action), command.RecordID, reading).Scan(&priorEntity, &priorID, &priorDay, &priorReading)
	if errors.Is(err, pgx.ErrNoRows) {
		return comparison, nil
	}
	if err != nil {
		return nil, err
	}
	comparison["occurred_on"], comparison["odometer"] = priorDay, priorReading
	comparison["source_entity"], comparison["source_id"] = priorEntity, priorID
	return comparison, nil
}

func (s *Store) fuelModulePlanPhysical(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data, vehicle Record, preview *FuelModulePreview) error {
	if command.Action == "charge.save" {
		settings, err := fuelModuleSettings(ctx, tx, id, command.Generation)
		if err != nil {
			return err
		}
		preview.Warnings = append(preview.Warnings, fuelModuleChargePlausibility(data, settings)...)
	}
	if command.Action == "charge.save" && String(data["odometer_series_id"]) == "" {
		preview.Warnings = append(preview.Warnings, FuelModuleWarning{Code: "charge_distance_unknown", Message: "Ladevorgang ohne Zählerbezug; dokumentierte Fahrenergie und Fahrverbrauch bleiben unbekannt", NextAction: "Energiebilanz getrennt von Fahrverbrauch bestätigen"})
		return nil
	}
	seriesID := String(data["odometer_series_id"])
	series, err := fuelModuleFact(ctx, tx, id, "odometer_series", seriesID, command.VehicleID)
	if err != nil {
		return err
	}
	reading, day := String(data["odometer"]), String(data["occurred_on"])
	if command.Action == "observation.save" {
		reading, day = String(data["reading"]), String(data["observed_on"])
	}
	if command.Action == "trip.save" {
		reading, day = String(data["start_odometer"]), String(data["started_on"])
	}
	proposed := Record{"odometer_series_id": seriesID, "odometer": reading}
	if err = fuelModulePhysicalScope(vehicle, series, proposed, day); err != nil {
		return err
	}
	previous, err := fuelModuleHistoryBounds(ctx, tx, id, command, seriesID, day, reading)
	if err != nil {
		return err
	}
	preview.After["current_comparison"] = Record{
		"previous": previous, "odometer_series_id": seriesID, "started_on": series["started_on"],
		"initial_reading": series["initial_reading"], "ended_on": series["ended_on"], "final_reading": series["final_reading"],
		"proposed_on": day, "proposed_reading": reading,
	}
	settings, err := fuelModuleSettings(ctx, tx, id, command.Generation)
	if err != nil {
		return err
	}
	comparison := Record{}
	for field, entry := range data {
		comparison[field] = entry
	}
	comparison["odometer"], comparison["occurred_on"] = reading, day
	preview.Warnings = append(preview.Warnings, fuelModulePlausibility(comparison, previous, settings)...)
	if command.Action == "fillup.save" {
		priceInput := func(field string) string {
			text, textErr := fuelModuleRawText(command.Data[field], 100)
			if textErr != nil {
				return ""
			}
			return text
		}
		calculation, _, calculationErr := fuelModulePrice(priceInput("quantity"), priceInput("unit_price"), priceInput("total_minor"))
		if calculationErr == nil {
			preview.After["calculation"] = calculation
		}
	}
	if command.Action == "trip.save" {
		proposed["odometer"] = String(data["end_odometer"])
		if err = fuelModulePhysicalScope(vehicle, series, proposed, String(data["ended_on"])); err != nil {
			return err
		}
		if _, err = fuelModuleHistoryBounds(ctx, tx, id, command, seriesID, String(data["ended_on"]), String(data["end_odometer"])); err != nil {
			return err
		}
		var overlap bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM fuel.trips WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3
 AND id<>coalesce(nullif($4,'')::uuid,'00000000-0000-0000-0000-000000000000') AND start_odometer<$6::numeric AND end_odometer>$5::numeric)`,
			id.HouseholdID, command.VehicleID, seriesID, command.RecordID, data["start_odometer"], data["end_odometer"]).Scan(&overlap)
		if err != nil {
			return err
		}
		if overlap {
			preview.Warnings = append(preview.Warnings, FuelModuleWarning{Code: "trip_overlap", Message: "Die Fahrt überschneidet sich mit dokumentierten Fahrstrecken", NextAction: "Zählerstände prüfen; überlappende Strecken werden nicht ungeprüft addiert"})
		}
	}
	return nil
}

func fuelModuleSaveFact(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, recordID string, version int64, data Record) (Record, error) {
	specifications := map[string]map[string]string{
		"vehicles":              {"name": "text", "registration": "text", "fuel_type": "text", "notes": "text", "next_service_on": "date", "next_inspection_on": "date", "insurance_renewal_on": "date", "tank_capacity": "numeric", "battery_capacity": "numeric", "current_series_id": "uuid", "archived_at": "timestamptz"},
		"odometer_series":       {"vehicle_id": "uuid", "started_on": "date", "ended_on": "date", "initial_reading": "numeric", "final_reading": "numeric", "reason": "text", "previous_series_id": "uuid", "distance_offset": "numeric"},
		"fillups":               {"vehicle_id": "uuid", "odometer_series_id": "uuid", "occurred_on": "date", "fuel_type": "text", "odometer": "numeric", "quantity": "numeric", "quantity_unit": "text", "total_minor": "bigint", "unit_price": "numeric", "currency_code": "text", "station": "text", "full_tank": "boolean", "previous_fill_missing": "boolean", "notes": "text", "source_kind": "text", "source_job_id": "uuid", "source_row": "integer", "source_fingerprint": "bytea"},
		"charging_sessions":     {"vehicle_id": "uuid", "odometer_series_id": "uuid", "occurred_on": "date", "odometer": "numeric", "energy_kwh": "numeric", "measurement_basis": "text", "total_minor": "bigint", "currency_code": "text", "location": "text", "charge_kind": "text", "soc_start": "numeric", "soc_end": "numeric", "notes": "text"},
		"trips":                 {"vehicle_id": "uuid", "odometer_series_id": "uuid", "started_on": "date", "ended_on": "date", "start_odometer": "numeric", "end_odometer": "numeric", "purpose": "text", "notes": "text"},
		"odometer_observations": {"vehicle_id": "uuid", "odometer_series_id": "uuid", "observed_on": "date", "reading": "numeric", "source_kind": "text", "capture_job_id": "uuid", "notes": "text"},
	}
	specification, ok := specifications[entity]
	if !ok {
		return nil, Invalid{"Nicht unterstützte Fahrzeugdatenart"}
	}
	keys := []string{}
	for key := range data {
		if _, supported := specification[key]; supported {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return nil, Invalid{"Keine Fahrzeugwerte angegeben"}
	}
	values := Record{}
	columns, definitions, selects := []string{}, []string{}, []string{}
	for _, key := range keys {
		quoted := pgx.Identifier{key}.Sanitize()
		columns = append(columns, quoted)
		definitions = append(definitions, quoted+" "+specification[key])
		selects = append(selects, "x."+quoted)
		entry := data[key]
		if entry != nil && String(entry) == "" && slices.Contains([]string{"date", "uuid", "numeric", "bigint", "integer"}, specification[key]) {
			entry = nil
		}
		values[key] = entry
	}
	encoded, err := json.Marshal(values)
	if err != nil || len(encoded) > 128<<10 {
		return nil, Invalid{"Fahrzeugwerte sind ungültig oder zu groß"}
	}
	var raw []byte
	table := pgx.Identifier{"fuel", entity}.Sanitize()
	if recordID == "" {
		recordID = value.UUID()
		err = tx.QueryRow(ctx, "INSERT INTO "+table+" AS t(id,household_id,created_by,"+strings.Join(columns, ",")+") SELECT $1::uuid,$2::uuid,$3::uuid,"+strings.Join(selects, ",")+" FROM jsonb_to_record($4::jsonb) x("+strings.Join(definitions, ",")+") RETURNING to_jsonb(t)", recordID, id.HouseholdID, id.UserID, encoded).Scan(&raw)
	} else {
		err = tx.QueryRow(ctx, "UPDATE "+table+" AS t SET("+strings.Join(columns, ",")+")=(SELECT "+strings.Join(selects, ",")+" FROM jsonb_to_record($4::jsonb) x("+strings.Join(definitions, ",")+")),version=version+1,updated_at=transaction_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING to_jsonb(t)", id.HouseholdID, recordID, version, encoded).Scan(&raw)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 256<<10 {
		return nil, Invalid{"Ergebnis überschreitet die zulässige Größe"}
	}
	return decode(raw)
}

func (s *Store) fuelModuleApplyCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, requestID string) (Record, error) {
	switch command.Action {
	case "vehicle.archive":
		var archivedAt any
		if data["archived"] == true {
			var at time.Time
			if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&at); err != nil {
				return nil, err
			}
			archivedAt = at.UTC().Format(time.RFC3339Nano)
		}
		record, err := fuelModuleSaveFact(ctx, tx, id, "vehicles", command.RecordID, command.Version, Record{"archived_at": archivedAt})
		if err != nil {
			return nil, err
		}
		if err = s.fuelModuleProjectFact(ctx, tx, id, "vehicles", record, command.Generation); err != nil {
			return nil, err
		}
		if err = Audit(ctx, tx, id, "fuel", "vehicles", command.RecordID, "record_write", requestID); err != nil {
			return nil, err
		}
		return record, nil
	case "settings.save":
		return fuelModuleSaveSettings(ctx, tx, id, command, data)
	case "import.stage":
		return s.fuelModuleStageImport(ctx, tx, id, command, data)
	case "import.confirm":
		return s.fuelModuleConfirmImport(ctx, tx, id, command, data, requestID)
	case "capture.stage":
		return s.fuelModuleStageCapture(ctx, tx, id, command, data)
	case "capture.confirm":
		return s.fuelModuleConfirmCapture(ctx, tx, id, command, data, requestID)
	case "odometer.replace":
		return s.fuelModuleReplaceOdometer(ctx, tx, id, command, data, requestID)
	case "vehicle.select_series":
		record, err := fuelModuleSaveFact(ctx, tx, id, "vehicles", command.RecordID, command.Version, Record{"current_series_id": data["odometer_series_id"]})
		if err != nil {
			return nil, err
		}
		if err = s.fuelModuleProjectFact(ctx, tx, id, "vehicles", record, command.Generation); err != nil {
			return nil, err
		}
		if err = Audit(ctx, tx, id, "fuel", "vehicles", command.RecordID, "record_write", requestID); err != nil {
			return nil, err
		}
		return record, nil
	}
	entity := fuelModuleEntity(command.Action)
	if entity == "" {
		return nil, Invalid{"Nicht unterstützter Fahrzeugvorgang"}
	}
	if strings.HasSuffix(command.Action, ".delete") {
		if err := fuelModuleAssertUnlinked(ctx, tx, id, entity, command.RecordID); err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM "+pgx.Identifier{"fuel", entity}.Sanitize()+" WHERE household_id=$1 AND vehicle_id=$2 AND id=$3 AND version=$4", id.HouseholdID, command.VehicleID, command.RecordID, command.Version)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrConflict
		}
		if err = s.fuelModuleProjectFact(ctx, tx, id, entity, Record{"id": command.RecordID, "deleted": true, "version": command.Version}, command.Generation); err != nil {
			return nil, err
		}
		if err = Audit(ctx, tx, id, "fuel", entity, command.RecordID, "record_delete", requestID); err != nil {
			return nil, err
		}
		return Record{"id": command.RecordID, "version": command.Version, "deleted": true}, nil
	}
	values := Record{}
	for field, entry := range data {
		values[field] = entry
	}
	if entity != "vehicles" {
		values["vehicle_id"] = command.VehicleID
	}
	if command.Action == "fillup.save" && command.RecordID == "" {
		values["source_kind"] = "manual"
	}
	if command.Action == "observation.save" && command.RecordID == "" {
		values["source_kind"] = "manual"
	}
	record, err := fuelModuleSaveFact(ctx, tx, id, entity, command.RecordID, command.Version, values)
	if err != nil {
		return nil, err
	}
	if err = s.fuelModuleProjectFact(ctx, tx, id, entity, record, command.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "fuel", entity, String(record["id"]), "record_write", requestID); err != nil {
		return nil, err
	}
	return record, nil
}

func fuelModulePreferenceKeys() []string {
	return []string{"default_vehicle_id", "default_series_id", "default_station", "minimum_consumption", "maximum_consumption", "maximum_fill_quantity", "maximum_daily_distance", "maximum_unit_price", "plausibility_unit", "plausibility_currency_code"}
}

func fuelModuleSaveSettings(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (Record, error) {
	keys := fuelModulePreferenceKeys()
	values := make([]any, 0, len(keys)+5)
	values = append(values, id.HouseholdID, id.UserID, command.Generation, command.Version)
	for _, key := range keys {
		if key == "default_station" {
			values = append(values, String(data[key]))
		} else {
			values = append(values, nullableFuel(data, key))
		}
	}
	var raw []byte
	err := tx.QueryRow(ctx, `INSERT INTO fuel.user_settings AS t(household_id,user_id,generation,default_vehicle_id,default_series_id,default_station,
 minimum_consumption,maximum_consumption,maximum_fill_quantity,maximum_daily_distance,maximum_unit_price,plausibility_unit,plausibility_currency_code)
 SELECT $1,$2,$3,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14 WHERE $4::bigint=0
 ON CONFLICT(household_id,user_id) DO UPDATE SET generation=excluded.generation,default_vehicle_id=excluded.default_vehicle_id,
 default_series_id=excluded.default_series_id,default_station=excluded.default_station,minimum_consumption=excluded.minimum_consumption,
 maximum_consumption=excluded.maximum_consumption,maximum_fill_quantity=excluded.maximum_fill_quantity,
 maximum_daily_distance=excluded.maximum_daily_distance,maximum_unit_price=excluded.maximum_unit_price,
 plausibility_unit=excluded.plausibility_unit,plausibility_currency_code=excluded.plausibility_currency_code,version=t.version+1,updated_at=transaction_timestamp()
 WHERE(t.version=$4 AND t.generation=$3) OR(t.generation<>$3 AND $4::bigint=0) RETURNING to_jsonb(t)-'household_id'-'user_id'`, values...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) && command.Version > 0 {
		err = tx.QueryRow(ctx, `UPDATE fuel.user_settings AS t SET default_vehicle_id=$5,default_series_id=$6,default_station=$7,
 minimum_consumption=$8,maximum_consumption=$9,maximum_fill_quantity=$10,maximum_daily_distance=$11,maximum_unit_price=$12,
 plausibility_unit=$13,plausibility_currency_code=$14,version=version+1,updated_at=transaction_timestamp()
 WHERE household_id=$1 AND user_id=$2 AND generation=$3 AND version=$4 RETURNING to_jsonb(t)-'household_id'-'user_id'`, values...).Scan(&raw)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	record, err := decode(raw)
	if err == nil {
		record["id"] = id.UserID
	}
	return record, err
}

func (s *Store) fuelModuleReplaceOdometer(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, requestID string) (Record, error) {
	old, err := fuelModuleFact(ctx, tx, id, "odometer_series", command.RecordID, command.VehicleID)
	if err != nil {
		return nil, err
	}
	closed, err := fuelModuleSaveFact(ctx, tx, id, "odometer_series", command.RecordID, command.Version, Record{"ended_on": data["closed_on"], "final_reading": data["final_reading"]})
	if err != nil {
		return nil, err
	}
	var offset any
	if previousOffset := String(old["distance_offset"]); previousOffset != "" {
		prior, priorErr := fuelModuleDecimal(previousOffset, false)
		initial, initialErr := fuelModuleDecimal(String(old["initial_reading"]), false)
		final, finalErr := fuelModuleDecimal(String(data["final_reading"]), false)
		if priorErr != nil || initialErr != nil || finalErr != nil {
			return nil, Invalid{"Bisheriger Streckenbezug ist ungültig; keine durchgehende Strecke erfinden"}
		}
		prior.Add(prior, new(big.Rat).Sub(final, initial))
		offset = fuelModuleDecimalString(prior)
	}
	created, err := fuelModuleSaveFact(ctx, tx, id, "odometer_series", "", 0, Record{
		"vehicle_id": command.VehicleID, "started_on": data["started_on"], "initial_reading": data["initial_reading"],
		"reason": data["reason"], "previous_series_id": command.RecordID, "distance_offset": offset,
	})
	if err != nil {
		return nil, err
	}
	vehicle, err := fuelModuleFact(ctx, tx, id, "vehicles", command.VehicleID, command.VehicleID)
	if err != nil {
		return nil, err
	}
	if String(vehicle["current_series_id"]) != command.RecordID {
		return nil, ErrConflict
	}
	updatedVehicle, err := fuelModuleSaveFact(ctx, tx, id, "vehicles", command.VehicleID, Int(vehicle["version"]), Record{"current_series_id": created["id"]})
	if err != nil {
		return nil, err
	}
	if err = s.fuelModuleProjectFact(ctx, tx, id, "vehicles", updatedVehicle, command.Generation); err != nil {
		return nil, err
	}
	if err = s.fuelModuleProjectFact(ctx, tx, id, "odometer_series", closed, command.Generation); err != nil {
		return nil, err
	}
	if err = s.fuelModuleProjectFact(ctx, tx, id, "odometer_series", created, command.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "fuel", "vehicles", command.VehicleID, "record_write", requestID); err != nil {
		return nil, err
	}
	created["previous_series_id"] = command.RecordID
	created["previous_final_reading"] = data["final_reading"]
	return created, nil
}
