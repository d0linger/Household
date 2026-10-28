package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func fuelModuleLegacyHash(entity, rid, action string, input Mutation) ([]byte, error) {
	encoded, err := json.Marshal([]any{"fuel-legacy-input-v1", entity, rid, action, input})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func (s *Store) fuelModuleLegacyReceipt(ctx context.Context, id auth.Identity, key string, generation int64, inputHash []byte, requestID string) (Record, bool, error) {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "fuel", generation)
	if err != nil {
		return nil, false, err
	}
	defer rollback(tx)
	digest := sha256.Sum256(append([]byte("fuel-legacy-v1"), inputHash...))
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT fuel.module_legacy_receipt($1,$2,$3,$4,$5,$6)`, key, generation, digest[:], id.SessionID, id.AuthVersion, id.MembershipID).Scan(&raw)
	if err != nil {
		return nil, false, fuelModuleError(err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, tx.Commit(ctx)
	}
	var envelope struct {
		State        string           `json:"state"`
		Result       FuelModuleResult `json:"result"`
		Dependencies map[string]int64 `json:"dependencies"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.State != "complete" {
		return nil, false, ErrConflict
	}
	if err = s.fuelModuleAuthorizeCoreResult(ctx, tx, id, envelope.Result, envelope.Dependencies); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return fuelModuleLegacyResult(envelope.Result), true, nil
}

func fuelModuleLegacyResult(result FuelModuleResult) Record {
	out := Record{}
	for name, entry := range result.Data {
		out[name] = entry
	}
	if String(out["id"]) == "" {
		out["id"] = result.RecordID
	}
	if out["version"] == nil {
		out["version"] = result.Version
	}
	out["message"] = result.Message
	out["operation_id"] = result.OperationID
	return out
}

func (s *Store) fuelModuleLegacyWrite(ctx context.Context, id auth.Identity, e module.Entity, rid, key, requestID string, input Mutation) (Record, error) {
	if !slices.Contains([]string{"vehicles", "odometer_series", "fillups"}, e.ID) {
		return nil, ErrNotFound
	}
	rid, key = strings.ToLower(rid), strings.ToLower(key)
	if !value.ValidUUID(key) {
		return nil, Invalid{"Gültigen Vorgangsschlüssel verwenden"}
	}
	hash, err := fuelModuleLegacyHash(e.ID, rid, "save", input)
	if err != nil {
		return nil, err
	}
	if receipt, exists, err := s.fuelModuleLegacyReceipt(ctx, id, key, input.Generation, hash, requestID); exists || err != nil {
		return receipt, err
	}
	data, err := e.Normalize(input.Data, rid != "")
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	vehicle := strings.ToLower(String(data["vehicle_id"]))
	if e.ID == "vehicles" {
		vehicle = rid
	}
	if rid != "" {
		if e.ID != "vehicles" {
			var actualVehicle string
			err = tx.QueryRow(ctx, "SELECT vehicle_id FROM "+pgx.Identifier{"fuel", e.ID}.Sanitize()+" WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&actualVehicle)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			if vehicle != "" && !strings.EqualFold(vehicle, actualVehicle) {
				return nil, Invalid{"Fahrzeugzuordnung bleibt erhalten; einen neuen passenden Datensatz ausdrücklich anlegen"}
			}
			vehicle = actualVehicle
		}
		before, err := fuelModuleCoreRecord(ctx, tx, id, e.ID, rid, vehicle, false)
		if err != nil {
			return nil, err
		}
		if Int(before["version"]) != input.Version {
			return nil, ErrConflict
		}
		for _, field := range e.Fields {
			if _, supplied := data[field.Name]; !supplied {
				data[field.Name] = before[field.Name]
			}
			if field.Kind != "boolean" && data[field.Name] != nil {
				data[field.Name] = String(data[field.Name])
			}
		}
		if e.ID != "vehicles" {
			vehicle = String(before["vehicle_id"])
		}
	}
	action := ""
	switch e.ID {
	case "vehicles":
		action = "vehicle.save"
	case "odometer_series":
		action = "series.save"
	case "fillups":
		action = "fillup.save"
	default:
		return nil, ErrNotFound
	}
	delete(data, "vehicle_id")
	if e.ID == "fillups" {
		data["total_minor"] = strconv.FormatInt(Int(data["total_minor"]), 10)
		unit := ""
		if err = tx.QueryRow(ctx, `SELECT unit_code FROM fuel.fuel_types WHERE code=$1 AND code<>'electric'`, data["fuel_type"]).Scan(&unit); err != nil {
			return nil, err
		}
		data["quantity_unit"] = unit
		data["currency_code"] = scope.Currency
	}
	for name, entry := range data {
		if entry == nil {
			data[name] = ""
		}
	}
	closed, err := fuelModuleRawValues(data)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	// Legacy forms send the whole module-generation map. These local writes
	// have no cross-module effect; the original map remains in the intent hash.
	command := FuelModuleCommand{Action: action, Generation: input.Generation, VehicleID: vehicle, RecordID: rid, Version: input.Version, SourceStamp: scope.Stamp, Data: closed}
	preview, err := s.FuelModulePreview(ctx, id, command)
	if err != nil {
		return nil, err
	}
	if len(preview.Warnings) > 0 {
		return nil, Invalid{"Fahrzeuge → Erfassung öffnen und die Berechnungs- oder Plausibilitätshinweise ausdrücklich prüfen"}
	}
	preview.Command.legacyHash = hash
	result, err := s.FuelModuleExecute(ctx, id, key, requestID, preview.Command)
	if err != nil {
		return nil, err
	}
	return fuelModuleLegacyResult(result), nil
}

func (s *Store) fuelModuleLegacyDelete(ctx context.Context, id auth.Identity, entity, rid string, version, generation int64, requestID string) error {
	rid = strings.ToLower(rid)
	if entity != "fillups" {
		return Invalid{"Fahrzeuge und Zählerreihen werden erhalten. Im Fahrzeugbereich archivieren beziehungsweise einen Tachowechsel prüfen"}
	}
	input := Mutation{Version: version, Generation: generation}
	hash, err := fuelModuleLegacyHash(entity, rid, "delete", input)
	if err != nil {
		return err
	}
	keyHash := sha256.Sum256(append([]byte(id.HouseholdID+":"+id.UserID+":"), hash...))
	keyText := hex.EncodeToString(keyHash[:16])
	key := keyText[:8] + "-" + keyText[8:12] + "-" + keyText[12:16] + "-" + keyText[16:20] + "-" + keyText[20:]
	if _, exists, err := s.fuelModuleLegacyReceipt(ctx, id, key, generation, hash, requestID); exists || err != nil {
		return err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollback(tx)
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return err
	}
	var vehicle string
	err = tx.QueryRow(ctx, `SELECT vehicle_id FROM fuel.fillups WHERE household_id=$1 AND id=$2 AND version=$3`, id.HouseholdID, rid, version).Scan(&vehicle)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	command := FuelModuleCommand{Action: "fillup.delete", Generation: generation, VehicleID: vehicle, RecordID: rid, Version: version, SourceStamp: scope.Stamp, Data: map[string]json.RawMessage{}}
	preview, err := s.FuelModulePreview(ctx, id, command)
	if err != nil {
		return err
	}
	preview.Command.legacyHash = hash
	_, err = s.FuelModuleExecute(ctx, id, key, requestID, preview.Command)
	return err
}

func (s *Store) fuelModuleLegacyAction(ctx context.Context, id auth.Identity, entity, rid, action, key, requestID string, input Mutation) (Record, error) {
	rid, key = strings.ToLower(rid), strings.ToLower(key)
	if entity != "fillups" || action != "expense" || !value.ValidUUID(key) || !value.ValidUUID(rid) || len(input.Data) != 1 {
		return nil, Invalid{"Diesen Fahrzeugvorgang im neuen Arbeitsbereich prüfen"}
	}
	hash, err := fuelModuleLegacyHash(entity, rid, action, input)
	if err != nil {
		return nil, err
	}
	if receipt, exists, err := s.fuelModuleLegacyReceipt(ctx, id, key, input.Generation, hash, requestID); exists || err != nil {
		return receipt, err
	}
	category, err := fuelModuleRawText(input.Data["category_id"], 36)
	if err != nil || !value.ValidUUID(category) {
		return nil, Invalid{"Ausgabenkategorie auswählen"}
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var vehicle string
	err = tx.QueryRow(ctx, `SELECT vehicle_id FROM fuel.fillups WHERE household_id=$1 AND id=$2 AND version=$3`, id.HouseholdID, rid, input.Version).Scan(&vehicle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	data, err := fuelModuleRawValues(Record{"source_entity": "fillups", "expense_category_id": category})
	if err != nil {
		return nil, err
	}
	command := FuelModuleCommand{Action: "cost.link_expense", Generation: input.Generation, VehicleID: vehicle, RecordID: rid, Version: input.Version, SourceStamp: scope.Stamp, Data: data, TargetGenerations: map[string]int64{"expenses": input.TargetGenerations["expenses"]}}
	preview, err := s.FuelModulePreview(ctx, id, command)
	if err != nil {
		return nil, err
	}
	preview.Command.legacyHash = hash
	result, err := s.FuelModuleExecute(ctx, id, key, requestID, preview.Command)
	if err != nil {
		return nil, err
	}
	return fuelModuleLegacyResult(result), nil
}
