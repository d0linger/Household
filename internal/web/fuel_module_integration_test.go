//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func fuelModuleNativeCommand(t *testing.T, f *integrationFixture, action, vehicle, rid string, version int64, data store.Record, targets map[string]int64) store.FuelModuleCommand {
	t.Helper()
	view, err := f.app.Store.FuelModuleWorkspace(f.ctx, f.identity, store.FuelModuleQuery{Pane: "overview", Limit: 1})
	if err != nil {
		t.Fatal("current Fuel command scope", err)
	}
	raw := map[string]json.RawMessage{}
	for field, entry := range data {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		raw[field] = encoded
	}
	return store.FuelModuleCommand{Action: action, Generation: view.Context.Generation, VehicleID: vehicle, RecordID: rid, Version: version,
		SourceStamp: view.Context.Stamp, Data: raw, TargetGenerations: targets}
}

func fuelModuleNativeReview(t *testing.T, f *integrationFixture, command store.FuelModuleCommand) store.FuelModulePreview {
	t.Helper()
	for range 4 {
		preview, err := f.app.Store.FuelModulePreview(f.ctx, f.identity, command)
		if err != nil {
			t.Fatalf("preview %s: %v", command.Action, err)
		}
		acknowledged := []string{}
		if raw := preview.Command.Data["acknowledged_warnings"]; len(raw) > 0 {
			if err = json.Unmarshal(raw, &acknowledged); err != nil {
				t.Fatal(err)
			}
		}
		missing := false
		for _, warning := range preview.Warnings {
			if warning.Code != "" && !slices.Contains(acknowledged, warning.Code) {
				acknowledged = append(acknowledged, warning.Code)
				missing = true
			}
		}
		if !missing {
			return preview
		}
		slices.Sort(acknowledged)
		command = preview.Command
		raw, err := json.Marshal(slices.Compact(acknowledged))
		if err != nil {
			t.Fatal(err)
		}
		command.Data["acknowledged_warnings"] = raw
	}
	t.Fatal("Fuel preview warnings did not stabilize")
	return store.FuelModulePreview{}
}

func fuelModuleNativeApply(t *testing.T, f *integrationFixture, action, vehicle, rid string, version int64, data store.Record, targets map[string]int64) (store.FuelModuleResult, store.FuelModuleCommand, string) {
	t.Helper()
	preview := fuelModuleNativeReview(t, f, fuelModuleNativeCommand(t, f, action, vehicle, rid, version, data, targets))
	key := value.UUID()
	result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), preview.Command)
	if err != nil {
		t.Fatalf("execute %s: %v", action, err)
	}
	if result.Action != action || !value.ValidUUID(result.OperationID) {
		t.Fatal("actual Fuel receipt identity missing")
	}
	return result, preview.Command, key
}

func fuelModuleNativeVehicle(t *testing.T, f *integrationFixture) (store.Record, store.Record) {
	t.Helper()
	vehicle, _, _ := fuelModuleNativeApply(t, f, "vehicle.save", "", "", 0, store.Record{"name": "SYNTHETIC Fuel integration vehicle", "fuel_type": "diesel", "registration": "TEST-ONLY"}, nil)
	series, _, _ := fuelModuleNativeApply(t, f, "series.save", vehicle.RecordID, "", 0, store.Record{"started_on": "2026-01-01", "initial_reading": "0", "reason": "SYNTHETIC original series"}, nil)
	selected, _, _ := fuelModuleNativeApply(t, f, "vehicle.select_series", vehicle.RecordID, vehicle.RecordID, vehicle.Version,
		store.Record{"odometer_series_id": series.RecordID, "series_version": series.Version}, nil)
	return selected.Data, series.Data
}

func fuelModuleNativeHTTP(t *testing.T, f *integrationFixture, method, path string, body any, key string) (int, store.Record, []byte) {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: f.app.cookieName(), Value: f.token})
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.server.URL)
	request.Header.Set("X-CSRF-Token", f.app.Auth.CSRF(f.token, "session"))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 32<<20 {
		t.Fatal("response exceeded the native test resource bound")
	}
	record := store.Record{}
	if len(raw) > 0 && response.Header.Get("Content-Type") != "text/csv; charset=utf-8" {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		_ = decoder.Decode(&record)
	}
	return response.StatusCode, record, raw
}

func TestFuelModuleNativeVehicleAndSignedReviewContract(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	command := fuelModuleNativeCommand(t, f, "fillup.save", store.String(vehicle["id"]), "", 0, store.Record{
		"odometer_series_id": series["id"], "occurred_on": "2026-01-02", "fuel_type": "diesel", "quantity_unit": "l",
		"odometer": "100", "quantity": "10", "total_minor": "2000", "currency_code": "EUR", "full_tank": true, "previous_fill_missing": false}, nil)
	key := value.UUID()
	if _, err := f.app.Store.FuelModulePreview(f.ctx, f.identity, command); err != nil {
		t.Fatal("direct first-fillup preview", err)
	}
	status, preview, _ := fuelModuleNativeHTTP(t, f, http.MethodPost, "/api/v1/fuel/module/preview", command, key)
	if status != http.StatusOK || store.String(preview["review"]) == "" {
		t.Fatalf("signed preview status=%d error=%s", status, store.String(preview["error"]))
	}
	review := store.String(preview["review"])
	status, _, _ = fuelModuleNativeHTTP(t, f, http.MethodPost, "/api/v1/fuel/module/execute", store.Record{"review": review}, value.UUID())
	if status == http.StatusOK {
		t.Fatal("signed review accepted another operation key")
	}
	status, result, _ := fuelModuleNativeHTTP(t, f, http.MethodPost, "/api/v1/fuel/module/execute", store.Record{"review": review}, key)
	if status != http.StatusOK {
		t.Fatalf("actual signed execution status=%d error=%s", status, store.String(result["error"]))
	}
	status, replayed, _ := fuelModuleNativeHTTP(t, f, http.MethodPost, "/api/v1/fuel/module/execute", store.Record{"review": review}, key)
	if status != http.StatusOK || replayed["operation_id"] != result["operation_id"] {
		t.Fatal("signed replay did not return the same accepted operation")
	}
	var count int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM fuel.fillups WHERE household_id=$1 AND vehicle_id=$2`, f.identity.HouseholdID, vehicle["id"]).Scan(&count); err != nil || count != 1 {
		t.Fatal("signed review duplicated its actual effect", count, err)
	}
}
