//go:build integration

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestPantryReportLimit(t *testing.T) {
	f := newIntegrationFixture(t)
	seed := func(first, last int) {
		t.Helper()
		_, err := f.owner.Exec(f.ctx, `INSERT INTO pantry.products(household_id,name,unit_code,created_by)
			SELECT $1,'Report fixture '||n,'piece',$2 FROM generate_series($3::integer,$4::integer) n`, f.identity.HouseholdID, f.identity.UserID, first, last)
		if err != nil {
			t.Fatal(err)
		}
	}
	seed(1, store.ReportProductLimit)
	assertReportCount(t, f, "pantry", "products", store.ReportProductLimit)
	seed(store.ReportProductLimit+1, store.ReportProductLimit+1)
	assertReportOverflow(t, f, "pantry", "100 Vorratsprodukte", "products")
}

func TestFuelReportLimit(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO fuel.vehicles(id,household_id,name,fuel_type,created_by) VALUES($1,$2,'Report vehicle','petrol',$3)", vehicle, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO fuel.odometer_series(id,household_id,vehicle_id,started_on,initial_reading,created_by) VALUES($1,$2,$3,'2026-01-01',0,$4)", series, f.identity.HouseholdID, vehicle, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	seed := func(first, last int) {
		t.Helper()
		for start := first; start <= last; start += 500 {
			end := min(start+499, last)
			_, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.fillups(household_id,vehicle_id,odometer_series_id,occurred_on,fuel_type,odometer,quantity,total_minor,created_by)
				SELECT $1,$2,$3,'2026-01-01','petrol',n,1,100,$4 FROM generate_series($5::integer,$6::integer) n`, f.identity.HouseholdID, vehicle, series, f.identity.UserID, start, end)
			if err != nil {
				t.Fatalf("seed fuel rows %d–%d: %v", start, end, err)
			}
		}
	}
	seed(1, store.ReportHistoryLimit)
	assertReportCount(t, f, "fuel", "fillups", store.ReportHistoryLimit)
	seed(store.ReportHistoryLimit+1, store.ReportHistoryLimit+1)
	assertReportOverflow(t, f, "fuel", "10.000 Tankvorgänge", "fillups", "consumption")
}

func TestMeterReportLimit(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := f.app.Store.SetModule(f.ctx, f.identity, "meters", true, f.app.Store.States()["meters"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	meter, device := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO meters.meters(id,household_id,name,unit_code,created_by) VALUES($1,$2,'Report meter','kWh',$3)", meter, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO meters.devices(id,household_id,meter_id,installed_on,initial_reading,created_by) VALUES($1,$2,$3,'2026-01-01',0,$4)", device, f.identity.HouseholdID, meter, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	seed := func(first, last int) {
		t.Helper()
		for start := first; start <= last; start += 500 {
			end := min(start+499, last)
			_, err := f.owner.Exec(f.ctx, `INSERT INTO meters.readings(household_id,device_id,measured_at,value,created_by)
				SELECT $1,$2,timestamptz '2026-01-01 00:00:00+00'+n*interval '1 minute',n,$3 FROM generate_series($4::integer,$5::integer) n`, f.identity.HouseholdID, device, f.identity.UserID, start, end)
			if err != nil {
				t.Fatalf("seed meter rows %d–%d: %v", start, end, err)
			}
		}
	}
	seed(1, store.ReportHistoryLimit)
	assertReportCount(t, f, "meters", "consumption", store.ReportHistoryLimit)
	seed(store.ReportHistoryLimit+1, store.ReportHistoryLimit+1)
	assertReportOverflow(t, f, "meters", "10.000 Zählerstände", "consumption")
}

func assertReportCount(t *testing.T, f *integrationFixture, module, field string, count int) {
	t.Helper()
	response, body, err := f.request(http.MethodGet, "/api/v1/m/"+module+"/reports/summary", f.token, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("at-limit report rejected: %d %.300s", response.StatusCode, body)
	}
	var report map[string][]json.RawMessage
	if err = json.Unmarshal(body, &report); err != nil || len(report[field]) != count {
		t.Fatalf("at-limit report contains %d records, want %d: %v", len(report[field]), count, err)
	}
}

func assertReportOverflow(t *testing.T, f *integrationFixture, module, message string, fields ...string) {
	t.Helper()
	response, body, err := f.request(http.MethodGet, "/api/v1/m/"+module+"/reports/summary", f.token, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]json.RawMessage
	if err = json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	var errorMessage string
	if err = json.Unmarshal(report["error"], &errorMessage); err != nil || response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(errorMessage, message) {
		t.Fatalf("overflow was not explained: status=%d message=%q error=%v", response.StatusCode, errorMessage, err)
	}
	for _, field := range fields {
		if _, present := report[field]; present {
			t.Fatalf("overflow returned partial %s results", field)
		}
	}
}
