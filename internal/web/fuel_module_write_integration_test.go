//go:build integration

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func fuelModuleWriteFillData(series, day, reading, quantity, minor string) store.Record {
	return store.Record{"odometer_series_id": series, "occurred_on": day, "fuel_type": "diesel", "odometer": reading,
		"quantity": quantity, "quantity_unit": "l", "total_minor": minor, "currency_code": "EUR", "full_tank": true, "previous_fill_missing": false}
}

func fuelModuleWriteSnapshot(t *testing.T, f *integrationFixture) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, relation := range [][2]string{
		{"fuel", "vehicles"}, {"fuel", "odometer_series"}, {"fuel", "fillups"}, {"fuel", "charging_sessions"}, {"fuel", "trips"},
		{"fuel", "odometer_observations"}, {"fuel", "vehicle_costs"}, {"fuel", "maintenance_plans"}, {"fuel", "maintenance_events"},
		{"fuel", "integration_bindings"}, {"fuel", "import_jobs"}, {"fuel", "import_rows"}, {"fuel", "capture_jobs"}, {"fuel", "capture_transitions"},
		{"fuel", "module_operations"}, {"fuel", "module_effects"}, {"fuel", "legacy_operation_fences"}, {"fuel", "household_state"},
		{"fuel", "segment_state"}, {"fuel", "segment_jobs"}, {"fuel", "user_settings"},
		{"core", "audit_log"}, {"core", "resource_links"}, {"core", "search_documents"}, {"core", "outbox_events"}, {"core", "reminders"}, {"core", "file_deletions"},
	} {
		name := pgx.Identifier{relation[0], relation[1]}.Sanitize()
		var digest string
		// Fixture-only owner observation never replaces an App business write.
		query := `SELECT encode(sha256(convert_to(coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text,'[]'),'UTF8')),'hex') FROM ` + name + ` r`
		if err := f.owner.QueryRow(f.ctx, query).Scan(&digest); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		result[relation[0]+"."+relation[1]] = digest
	}
	return result
}

func fuelModuleWriteUnchanged(t *testing.T, f *integrationFixture, before map[string]string) {
	t.Helper()
	after := fuelModuleWriteSnapshot(t, f)
	for relation, digest := range before {
		if after[relation] != digest {
			t.Errorf("refused/replayed operation changed %s", relation)
		}
	}
}

func fuelModuleWriteSQLCode(t *testing.T, err error, code string) {
	t.Helper()
	var problem *pgconn.PgError
	if !errors.As(err, &problem) || problem.Code != code {
		t.Fatalf("SQLSTATE = %v; want %s", err, code)
	}
}

func fuelModuleWriteRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func fuelModuleWriteReview(t *testing.T, f *integrationFixture, action, vehicle, rid string, version int64, data store.Record) store.FuelModuleCommand {
	t.Helper()
	command := fuelModuleNativeCommand(t, f, action, vehicle, rid, version, data, nil)
	for range 3 {
		preview, err := f.app.Store.FuelModulePreview(f.ctx, f.identity, command)
		if err != nil {
			t.Fatalf("review %s: %v", action, err)
		}
		codes := make([]string, 0, len(preview.Warnings))
		for _, warning := range preview.Warnings {
			if warning.Code != "" {
				codes = append(codes, warning.Code)
			}
		}
		command = preview.Command
		ack, err := json.Marshal(codes)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(command.Data["acknowledged_warnings"], ack) || len(codes) == 0 {
			return command
		}
		command.Data["acknowledged_warnings"] = ack
	}
	t.Fatal("warning acknowledgement did not stabilize after renewed preview")
	return store.FuelModuleCommand{}
}

func TestFuelModuleNativeDurableReplayChangedBodyAndDeletedFact(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	accepted, command, key := fuelModuleNativeApply(t, f, "fillup.save", store.String(vehicle["id"]), "", 0,
		fuelModuleWriteFillData(store.String(series["id"]), "2026-02-01", "1000", "40", "6800"), nil)
	if accepted.RecordID == "" || accepted.Version != 1 || store.String(accepted.Data["total_minor"]) != "6800" || store.String(accepted.Data["source_kind"]) != "manual" {
		t.Fatal("actual manual fact receipt missing", accepted)
	}
	before := fuelModuleWriteSnapshot(t, f)
	replayed, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, strings.ToUpper(key), value.UUID(), command)
	if err != nil || !reflect.DeepEqual(accepted, replayed) {
		t.Fatal("same key/body did not replay the exact historical receipt", replayed, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
	changed := command
	changed.Data = make(map[string]json.RawMessage, len(command.Data))
	for field, raw := range command.Data {
		changed.Data[field] = raw
	}
	changed.Data["total_minor"] = json.RawMessage(`"6801"`)
	if _, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), changed); err == nil {
		t.Fatal("same key accepted a different reviewed amount")
	}
	fuelModuleWriteUnchanged(t, f, before)
	fuelModuleNativeApply(t, f, "fillup.delete", store.String(vehicle["id"]), accepted.RecordID, accepted.Version, store.Record{}, nil)
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE module_id='fuel'"); err != nil {
		t.Fatal(err)
	}
	before = fuelModuleWriteSnapshot(t, f)
	replayed, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil || !reflect.DeepEqual(accepted, replayed) {
		t.Fatal("fact deletion/cache pruning destroyed retained intent", replayed, err)
	}
	status, err := f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, command.Generation)
	if err != nil || status["state"] != "complete" {
		t.Fatal("accepted operation status missing", status, err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM fuel.fillups WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, accepted.RecordID).Scan(&count); err != nil || count != 0 {
		t.Fatal("historical replay recreated a deleted fillup", count, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
}

func TestFuelModuleNativeReplayRequiresCurrentWriterAndGeneration(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	_, command, key := fuelModuleNativeApply(t, f, "fillup.save", store.String(vehicle["id"]), "", 0,
		fuelModuleWriteFillData(store.String(series["id"]), "2026-02-01", "1000", "40", "6800"), nil)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET role='reader' WHERE id=$1", f.identity.MembershipID); err != nil {
		t.Fatal(err)
	}
	before := fuelModuleWriteSnapshot(t, f)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), command); !errors.Is(err, store.ErrForbidden) || result.OperationID != "" {
		t.Fatal("stale cached editor role replayed private writer receipt", result, err)
	}
	if status, err := f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, command.Generation); !errors.Is(err, store.ErrForbidden) || status != nil {
		t.Fatal("downgraded writer obtained operation status", status, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET role='admin' WHERE id=$1", f.identity.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET data_generation=data_generation+1 WHERE household_id=$1 AND module_id='fuel'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	before = fuelModuleWriteSnapshot(t, f)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), command); err == nil || result.OperationID != "" {
		t.Fatal("old generation accepted retained receipt", result, err)
	}
	if status, err := f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, command.Generation); err == nil || status != nil {
		t.Fatal("old generation exposed operation status", status, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
}

func TestFuelModuleNativeProtocolRejectsRRUnfinishedAndPrivateProofWrites(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelModuleNativeVehicle(t, f)
	generation := f.app.Store.States()["fuel"].Generation
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	before := fuelModuleWriteSnapshot(t, f)
	begin := func(tx pgx.Tx) error {
		t.Helper()
		hash := sha256.Sum256([]byte("actual App incomplete protocol probe"))
		_, err := tx.Exec(ctx, "SELECT fuel.module_begin($1,$2,$3,'fillup.save',$4,'{}',$5,$6,$7)", value.UUID(), value.UUID(), generation, hash[:], f.identity.SessionID, f.identity.AuthVersion, f.identity.MembershipID)
		return err
	}
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if err = audit.Bind(ctx, tx); err != nil {
		fuelModuleWriteRollback(tx)
		t.Fatal(err)
	}
	fuelModuleWriteSQLCode(t, begin(tx), "23514")
	fuelModuleWriteRollback(tx)
	fuelModuleWriteUnchanged(t, f, before)
	tx, err = audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err = begin(tx); err != nil {
		fuelModuleWriteRollback(tx)
		t.Fatal("actual authorized RC reservation failed", err)
	}
	err = tx.Commit(ctx)
	fuelModuleWriteRollback(tx)
	fuelModuleWriteSQLCode(t, err, "23514")
	fuelModuleWriteUnchanged(t, f, before)
	for _, statement := range []string{
		"SELECT * FROM fuel.module_operations", "SELECT * FROM fuel.module_effects", "SELECT * FROM fuel.capture_transitions", "SELECT * FROM fuel.legacy_operation_fences",
		"INSERT INTO fuel.capture_transitions(transaction_id,job_id,before_sha256,after_sha256) VALUES(pg_current_xact_id(),gen_random_uuid(),decode(repeat('00',32),'hex'),decode(repeat('00',32),'hex'))",
	} {
		_, err = f.pool.Exec(ctx, statement)
		fuelModuleWriteSQLCode(t, err, "42501")
	}
	tx, err = f.app.Store.BeginMutation(ctx, f.identity, "fuel", generation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, "UPDATE fuel.vehicles SET name='Unreserved edit',version=version+1 WHERE household_id=$1", f.identity.HouseholdID)
	fuelModuleWriteRollback(tx)
	fuelModuleWriteSQLCode(t, err, "42501")
	fuelModuleWriteUnchanged(t, f, before)
}

func TestFuelModuleNativeUnknownStatusAndExplicitTerminalAbandon(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	command := fuelModuleWriteReview(t, f, "fillup.save", store.String(vehicle["id"]), "", 0,
		fuelModuleWriteFillData(store.String(series["id"]), "2026-02-01", "1000", "40", "6800"))
	key, op := value.UUID(), value.UUID()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer fuelModuleWriteRollback(tx)
	hash := sha256.Sum256([]byte("in-flight actual App reservation, rolled back below"))
	_, err = tx.Exec(ctx, "SELECT fuel.module_begin($1,$2,$3,'fillup.save',$4,'{}',$5,$6,$7)", op, key, command.Generation, hash[:], f.identity.SessionID, f.identity.AuthVersion, f.identity.MembershipID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, command.Generation)
	if err != nil || status["state"] != "unknown" || status["result"] != nil {
		t.Fatal("in-flight reservation falsely reported absent or complete", status, err)
	}
	fuelModuleWriteRollback(tx)
	status, err = f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, command.Generation)
	if err != nil || status["state"] != "unknown" {
		t.Fatal("uncertain key should require an explicit recovery decision", status, err)
	}
	refused, err := f.app.Store.FuelModuleAbandonOperation(f.ctx, f.identity, key, command.Generation, value.UUID())
	if err != nil || refused["state"] != "refused" {
		t.Fatal("explicit abandonment did not install a terminal fence", refused, err)
	}
	before := fuelModuleWriteSnapshot(t, f)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), command); err == nil || result.OperationID != "" {
		t.Fatal("abandoned key accepted a late business mutation", result, err)
	}
	status, err = f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, command.Generation)
	if err != nil || status["state"] != "refused" {
		t.Fatal("terminal key status was lost", status, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
}

func TestFuelModuleNativeChargeTripArchiveAndOdometerHandover(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	unacknowledged := fuelModuleNativeCommand(t, f, "charge.save", vid, "", 0, store.Record{
		"occurred_on": "2026-02-01", "energy_kwh": "3", "measurement_basis": "unknown", "currency_code": "EUR", "charge_kind": "home"}, nil)
	preview, err := f.app.Store.FuelModulePreview(f.ctx, f.identity, unacknowledged)
	if err != nil || len(preview.Warnings) == 0 {
		t.Fatal("unknown distance charge did not require an explicit review", preview, err)
	}
	delete(preview.Command.Data, "acknowledged_warnings")
	before := fuelModuleWriteSnapshot(t, f)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, value.UUID(), value.UUID(), preview.Command); err == nil || result.OperationID != "" {
		t.Fatal("unacknowledged charge warning created a business fact", result, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
	for index, cost := range []any{nil, "0", "1234"} {
		result, _, _ := fuelModuleNativeApply(t, f, "charge.save", vid, "", 0,
			store.Record{"occurred_on": "2026-02-01", "energy_kwh": "3.123456", "measurement_basis": "wall", "total_minor": cost, "currency_code": "EUR", "charge_kind": "home", "notes": fmt.Sprintf("Charge distinction %d", index)}, nil)
		if result.Data["total_minor"] != cost || result.Data["measurement_basis"] != "wall" || result.Data["odometer"] != nil {
			t.Fatal("charging invented cost/distance or lost measurement basis", result)
		}
	}
	trip, _, _ := fuelModuleNativeApply(t, f, "trip.save", vid, "", 0, store.Record{"odometer_series_id": sid,
		"started_on": "2026-02-01", "ended_on": "2026-02-02", "start_odometer": "100", "end_odometer": "150.123456", "purpose": "Actual household trip"}, nil)
	if store.String(trip.Data["end_odometer"]) != "150.123456" {
		t.Fatal("trip exact reading lost", trip)
	}
	var vehicleVersion int64
	if err := f.pool.QueryRow(f.ctx, "SELECT version FROM fuel.vehicles WHERE id=$1", vid).Scan(&vehicleVersion); err != nil {
		t.Fatal(err)
	}
	archived, _, _ := fuelModuleNativeApply(t, f, "vehicle.archive", vid, vid, vehicleVersion, store.Record{"archived": true}, nil)
	before = fuelModuleWriteSnapshot(t, f)
	blocked := fuelModuleNativeCommand(t, f, "fillup.save", vid, "", 0, fuelModuleWriteFillData(sid, "2026-02-03", "200", "40", "6800"), nil)
	if _, err := f.app.Store.FuelModulePreview(f.ctx, f.identity, blocked); err == nil {
		t.Fatal("archived vehicle accepted a new fillup review")
	}
	fuelModuleWriteUnchanged(t, f, before)
	fuelModuleNativeApply(t, f, "vehicle.archive", vid, vid, archived.Version, store.Record{"archived": false}, nil)
	handover, _, _ := fuelModuleNativeApply(t, f, "odometer.replace", vid, sid, store.Int(series["version"]), store.Record{
		"closed_on": "2026-06-30", "final_reading": "200", "started_on": "2026-07-01", "initial_reading": "0", "reason": "Documented physical replacement"}, nil)
	var ended, final, current, previous string
	var unknownOffset bool
	err = f.pool.QueryRow(f.ctx, `SELECT old.ended_on::text,old.final_reading::text,v.current_series_id::text,n.previous_series_id::text,n.distance_offset IS NULL
 FROM fuel.vehicles v JOIN fuel.odometer_series old ON old.id=$2 JOIN fuel.odometer_series n ON n.id=v.current_series_id WHERE v.id=$1`, vid, sid).Scan(&ended, &final, &current, &previous, &unknownOffset)
	if err != nil || ended != "2026-06-30" || final != "200.000000" || current != handover.RecordID || previous != sid || !unknownOffset {
		t.Fatal("handover lost old facts or invented continuity", ended, final, current, previous, unknownOffset, err)
	}
	fuelModuleNativeApply(t, f, "fillup.save", vid, "", 0, fuelModuleWriteFillData(current, "2026-07-02", "10", "1", "100"), nil)
	before = fuelModuleWriteSnapshot(t, f)
	staleSeries := fuelModuleNativeCommand(t, f, "fillup.save", vid, "", 0, fuelModuleWriteFillData(sid, "2026-07-02", "201", "1", "100"), nil)
	if _, err = f.app.Store.FuelModulePreview(f.ctx, f.identity, staleSeries); err == nil {
		t.Fatal("post-handover fact entered closed old series")
	}
	fuelModuleWriteUnchanged(t, f, before)
}

func fuelModuleWriteCSV(t *testing.T, f *integrationFixture, vehicle, series, text string) store.FuelModuleResult {
	t.Helper()
	result, _, _ := fuelModuleNativeApply(t, f, "import.stage", vehicle, "", 0, store.Record{"profile": "household", "csv_text": text,
		"currency_code": "EUR", "fuel_type": "diesel", "quantity_unit": "l", "history_complete": true, "odometer_series_id": series}, nil)
	return result
}

func fuelModuleWriteCSVDecisions(t *testing.T, f *integrationFixture, job string) []store.Record {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, "SELECT r.row_number::text,encode(r.source_fingerprint,'hex') FROM fuel.import_rows r WHERE r.job_id=$1 ORDER BY r.row_number", job)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	decisions := []store.Record{}
	for rows.Next() {
		var number, fingerprint string
		if err := rows.Scan(&number, &fingerprint); err != nil {
			t.Fatal(err)
		}
		decisions = append(decisions, store.Record{"row_number": number, "fingerprint": fingerprint, "decision": "import", "duplicate_ack": false})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return decisions
}

func TestFuelModuleNativeCSVStageConfirmDedupAndProcessedFence(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	text := "occurred_on,odometer,quantity,total_minor,full_tank,previous_fill_missing\n2026-02-01,1000,1,100,true,false\n2026-02-01,1000,1,100,true,false\n2026-02-01,invalid,1,100,true,false\n"
	staged := fuelModuleWriteCSV(t, f, vid, sid, text)
	var fills, stagedRows, errorsRows int
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM fuel.fillups),(SELECT count(*) FROM fuel.import_rows WHERE job_id=$1),
 (SELECT count(*) FROM fuel.import_rows WHERE job_id=$1 AND errors<>'[]'::jsonb)`, staged.RecordID).Scan(&fills, &stagedRows, &errorsRows); err != nil || fills != 0 || stagedRows != 3 || errorsRows != 1 {
		t.Fatal("staging created business facts or lost invalid source row", fills, stagedRows, errorsRows, err)
	}
	decisions := fuelModuleWriteCSVDecisions(t, f, staged.RecordID)
	decisions[1]["decision"], decisions[2]["decision"] = "skip", "skip"
	data := store.Record{"job_id": staged.RecordID, "job_version": strconv.FormatInt(staged.Version, 10), "decisions": decisions}
	accepted, command, key := fuelModuleNativeApply(t, f, "import.confirm", vid, "", 0, data, nil)
	if accepted.Data["state"] != "complete" || accepted.Data["imported"] != "1" || accepted.Data["skipped"] != "2" {
		t.Fatal("explicit import/skip decisions not applied", accepted)
	}
	var fillID string
	var version int64
	var sourceRow int
	if err := f.pool.QueryRow(f.ctx, "SELECT id::text,version,source_row FROM fuel.fillups WHERE source_job_id=$1", staged.RecordID).Scan(&fillID, &version, &sourceRow); err != nil || sourceRow != 1 {
		t.Fatal("import provenance not bound to processed row", sourceRow, err)
	}
	fuelModuleNativeApply(t, f, "fillup.delete", vid, fillID, version, store.Record{}, nil)
	before := fuelModuleWriteSnapshot(t, f)
	replay, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil || !reflect.DeepEqual(accepted, replay) {
		t.Fatal("accepted import receipt lost after fact deletion", replay, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
	current := fuelModuleNativeCommand(t, f, "import.confirm", vid, "", 0, store.Record{"job_id": staged.RecordID, "job_version": strconv.FormatInt(accepted.Version, 10), "decisions": decisions}, nil)
	if _, err = f.app.Store.FuelModulePreview(f.ctx, f.identity, current); err == nil {
		t.Fatal("new key could recreate an already processed import row")
	}
	restage := fuelModuleNativeCommand(t, f, "import.stage", vid, "", 0, store.Record{"profile": "household", "csv_text": text,
		"currency_code": "EUR", "fuel_type": "diesel", "quantity_unit": "l", "history_complete": true, "odometer_series_id": sid}, nil)
	if _, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, value.UUID(), value.UUID(), restage); err == nil {
		t.Fatal("processed source file was restaged as a fresh intent")
	}
	fuelModuleWriteUnchanged(t, f, before)
}

func TestFuelModuleNativeCSVHundredRowsRollbackAtFinalDuplicate(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	fuelModuleNativeApply(t, f, "fillup.save", vid, "", 0, fuelModuleWriteFillData(sid, "2026-02-01", "199", "1", "100"), nil)
	var csv strings.Builder
	csv.WriteString("occurred_on,odometer,quantity,total_minor,full_tank,previous_fill_missing\n")
	for index := range 100 {
		fmt.Fprintf(&csv, "2026-02-01,%d,1,100,true,false\n", index+100)
	}
	staged := fuelModuleWriteCSV(t, f, vid, sid, csv.String())
	decisions := fuelModuleWriteCSVDecisions(t, f, staged.RecordID)
	if len(decisions) != 100 {
		t.Fatal("test requires actual one hundred-row transaction", len(decisions))
	}
	for index, decision := range decisions {
		if decision["row_number"] != strconv.Itoa(index+1) {
			t.Fatalf("test requires numeric source order, not lexicographic text order: index=%d row=%v", index, decision["row_number"])
		}
	}
	var duplicateRow int
	if err := f.pool.QueryRow(f.ctx, "SELECT row_number FROM fuel.import_rows WHERE job_id=$1 AND duplicate_of IS NOT NULL", staged.RecordID).Scan(&duplicateRow); err != nil || duplicateRow != 100 {
		t.Fatal("test requires its one existing duplicate to be the final source row", duplicateRow, err)
	}
	data := store.Record{"job_id": staged.RecordID, "job_version": strconv.FormatInt(staged.Version, 10), "decisions": decisions}
	command := fuelModuleWriteReview(t, f, "import.confirm", vid, "", 0, data)
	before := fuelModuleWriteSnapshot(t, f)
	_, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, value.UUID(), value.UUID(), command)
	var invalid store.Invalid
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Error(), "Dublettenentscheidung") {
		t.Fatal("expected final-row duplicate decision denial after earlier writes", err)
	}
	fuelModuleWriteUnchanged(t, f, before)
	decisions[99]["duplicate_ack"] = true
	accepted, replayCommand, key := fuelModuleNativeApply(t, f, "import.confirm", vid, "", 0, data, nil)
	if accepted.Data["imported"] != "100" || accepted.Data["state"] != "complete" {
		t.Fatal("explicit reviewed hundred-row transaction incomplete", accepted)
	}
	var fills, processed, sourceRows int
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM fuel.fillups WHERE vehicle_id=$2),
 (SELECT count(*) FROM fuel.import_rows WHERE job_id=$1 AND decision='import'),
 (SELECT count(DISTINCT source_row) FROM fuel.fillups WHERE source_job_id=$1)`, staged.RecordID, vid).Scan(&fills, &processed, &sourceRows); err != nil || fills != 101 || processed != 100 || sourceRows != 100 {
		t.Fatal("hundred-row commit lost provenance or duplicated an unintended fact", fills, processed, sourceRows, err)
	}
	before = fuelModuleWriteSnapshot(t, f)
	replayed, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), replayCommand)
	if err != nil || !reflect.DeepEqual(accepted, replayed) {
		t.Fatal("hundred-row operation did not replay one accepted receipt", replayed, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
}
