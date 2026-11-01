//go:build integration

package web

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFuelOverviewManualDatesProfileAuditAndReaderRole(t *testing.T) {
	f := newIntegrationFixture(t)
	v, _ := fuelOverviewFixture(t, f, "manual dates")
	m, _ := f.app.Store.Module("fuel")
	e, _ := m.Entity("vehicles")
	in := auditMutation(t, f, "fuel", store.Record{"next_service_on": "2026-03-29", "next_inspection_on": "2026-10-25", "insurance_renewal_on": "2027-01-01"}, store.Int(v["version"]))
	updated, err := f.app.Store.Write(f.ctx, f.identity, m, e, store.String(v["id"]), value.UUID(), value.UUID(), in)
	if err != nil || updated["next_service_on"] != "2026-03-29" {
		t.Fatal(updated, err)
	}
	out := fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "manual dates"})
	if len(out.Vehicles[0].Dates) != 3 || out.Vehicles[0].Dates[0].State != "overdue" {
		t.Fatal(out)
	}
	var audited bool
	if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.audit_log WHERE resource_id=$1 AND resource_type='vehicles' AND action='UPDATE')", v["id"]).Scan(&audited); err != nil || !audited {
		t.Fatal(audited, err)
	}
	reader := shoppingTestMember(t, f, "overview-dates-reader", "reader")
	in.Version = store.Int(updated["version"])
	if _, err = f.app.Store.Write(f.ctx, reader, m, e, store.String(v["id"]), value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader changed date", err)
	}
	scope, err := f.app.Store.FuelOverviewContext(f.ctx, reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.FuelOverview(f.ctx, reader, scope, store.FuelOverviewQuery{}); err != nil {
		t.Fatal("reader cannot inspect manual I0 domain", err)
	}
	r, raw, err := f.request(http.MethodGet, "/m/fuel/overview", f.token, "", "", nil, nil)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(raw), "fuel-overview.js") || !strings.Contains(r.Header.Get("Permissions-Policy"), "camera=()") {
		t.Fatal(r, string(raw), err)
	}
}

func TestFuelOverviewHistoryAndSeriesLimitsDoNotReturnPartialRatios(t *testing.T) {
	f := newIntegrationFixture(t)
	v, s := fuelOverviewFixture(t, f, "budget")
	// Bounded restored-history fixtures retain the real invalidation triggers;
	// they do not impersonate thousands of accepted user operations.
	for start := 1; start <= 10001; start += 500 {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.fillups(household_id,vehicle_id,odometer_series_id,occurred_on,
 fuel_type,odometer,quantity,quantity_unit,total_minor,currency_code,created_by)
 SELECT $1,$2,$3,'2026-01-02','diesel',g::numeric/1000000,1,'l',100,'EUR',$4
 FROM generate_series($5::integer,$6::integer) g`, f.identity.HouseholdID, v["id"], s["id"], f.identity.UserID, start, min(start+499, 10001)); err != nil {
			t.Fatal("bounded prior fill history", start, err)
		}
	}
	out := fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "budget"})
	row := out.Vehicles[0]
	if !out.HistoryLimited || row.Consumption != nil || row.ConsumptionState != "segments_pending" || row.Odometer == nil || row.Last == nil {
		t.Fatal(out)
	}
	for start := 1; start <= 1000; start += 500 {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.odometer_series(household_id,vehicle_id,started_on,ended_on,initial_reading,created_by)
 SELECT $1,$2,'2025-01-01','2025-12-31',0,$3 FROM generate_series($4::integer,$5::integer) g`,
			f.identity.HouseholdID, v["id"], f.identity.UserID, start, min(start+499, 1000)); err != nil {
			t.Fatal("bounded closed-series fixture", err)
		}
	}
	out = fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "budget"})
	row = out.Vehicles[0]
	if out.SeriesLimited || row.Series == nil || row.Series.ID != s["id"] || row.Odometer == nil || row.Consumption != nil || row.ConsumptionState != "segments_pending" {
		t.Fatal("closed historical series exhausted the current-series budget", out)
	}
	// The actual bound now applies to applicable series, not closed history.
	for start := 1; start <= 1000; start += 500 {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.odometer_series(household_id,vehicle_id,started_on,initial_reading,reason,created_by)
 SELECT $1,$2,'2026-01-01',0,'SYNTHETIC prior overlapping applicable series',$3 FROM generate_series($4::integer,$5::integer) g`,
			f.identity.HouseholdID, v["id"], f.identity.UserID, start, min(start+499, 1000)); err != nil {
			t.Fatal("bounded applicable-series overflow fixture", err)
		}
	}
	out = fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "budget"})
	row = out.Vehicles[0]
	if !out.SeriesLimited || row.Series != nil || row.Odometer != nil || row.Consumption != nil || row.SeriesState != "series_limit" || row.Last == nil {
		t.Fatal(out)
	}
}

func TestFuelOverviewReadyPinsDateAndIndexShape(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct{ name, change, repair string }{
		{"index", "ALTER INDEX fuel.fillups_overview_series RENAME TO hidden_overview_series", "ALTER INDEX fuel.hidden_overview_series RENAME TO fillups_overview_series"},
		{"default", "ALTER TABLE fuel.vehicles ALTER COLUMN next_service_on SET DEFAULT '2027-01-01'", "ALTER TABLE fuel.vehicles ALTER COLUMN next_service_on DROP DEFAULT"},
		{"read privilege", "REVOKE SELECT ON fuel.vehicles FROM household_app", "GRANT SELECT ON fuel.vehicles TO household_app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, tc.change); err != nil {
				t.Fatal(err)
			}
			var ready bool
			if err := f.pool.QueryRow(f.ctx, "SELECT coalesce(fuel.overview_ready(),false)").Scan(&ready); err != nil || ready {
				t.Fatal(ready, err)
			}
			if _, err := f.owner.Exec(f.ctx, tc.repair); err != nil {
				t.Fatal(err)
			}
			if err := database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal(err)
			}
		})
	}
	v, _ := fuelOverviewFixture(t, f, "date constraint")
	// Probe the physical CHECK independently of the App operation guard.
	_, err := f.owner.Exec(f.ctx, "UPDATE fuel.vehicles SET next_service_on='2200-01-01' WHERE id=$1", v["id"])
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" {
		t.Fatal("out-of-range manual date accepted", err)
	}
}

func TestFuelOverviewGenuineFuel1UpgradePreservesAllPriorFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelModuleUpgradePriorCatalog(t, f, 1)
	vehicle := value.UUID()
	// Clearly bounded owner seed in the genuine prior Fuel1 schema. No modern
	// operation receipt is manufactured for an old fact.
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO fuel.vehicles(id,household_id,name,fuel_type,notes,created_by) VALUES($1,$2,'Old vehicle','diesel','Original note',$3)", vehicle, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.pool.QueryRow(f.ctx, "SELECT to_jsonb(v)::text FROM fuel.vehicles v WHERE id=$1", vehicle).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	var after string
	var unknown bool
	if err := f.pool.QueryRow(f.ctx, `SELECT (to_jsonb(v)-ARRAY['next_service_on','next_inspection_on','insurance_renewal_on','tank_capacity','battery_capacity','current_series_id'])::text,
 next_service_on IS NULL AND next_inspection_on IS NULL AND insurance_renewal_on IS NULL
 AND tank_capacity IS NULL AND battery_capacity IS NULL AND current_series_id IS NULL FROM fuel.vehicles v WHERE id=$1`, vehicle).Scan(&after, &unknown); err != nil || before != after || !unknown {
		t.Fatal("invented prior dates or changed data", before, after, unknown, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestFuelOverviewExplicitReviewedCoreFutureVisibilityBarrier(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelOverviewFixture(t, f, "future visibility")
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1", database.ExpectedVersion("core")+1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.FuelOverviewContext(f.ctx, f.identity); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("future physical visibility guessed", err)
	}
	var ready bool
	if err := f.pool.QueryRow(f.ctx, "SELECT coalesce(fuel.overview_ready(),false)").Scan(&ready); err != nil || ready {
		t.Fatal(ready, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1", database.ExpectedVersion("core")); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
