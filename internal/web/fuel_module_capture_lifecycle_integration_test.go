//go:build integration

package web

import (
	"context"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestFuelModuleCaptureExpiresTerminalJobsBeforeExtraction(t *testing.T) {
	for _, kind := range []string{"expired_queued", "exhausted_expired_lease"} {
		t.Run(kind, func(t *testing.T) {
			f := newIntegrationFixture(t)
			vehicle, series := fuelModuleNativeVehicle(t, f)
			attachment := fuelModuleCaptureUpload(t, f, store.String(vehicle["id"]))
			f.app.Store.FuelOCREnabled = true
			job := fuelModuleCaptureStage(t, f, store.String(vehicle["id"]), store.String(series["id"]), attachment)
			if job.Data["state"] != "queued" {
				t.Fatal("test requires an actual authorized queued capture", job)
			}
			// Owner-only fault injection models elapsed wall time / a crashed final
			// lease. The upload and staged job are genuine App operations. No private
			// worker capability, accepted receipt, or business fact is manufactured.
			var version int64
			var err error
			if kind == "expired_queued" {
				err = f.owner.QueryRow(f.ctx, `UPDATE fuel.capture_jobs SET expires_at=clock_timestamp()-interval '1 second',
 next_attempt_at=clock_timestamp()+interval '1 day',version=version+1 WHERE id=$1 RETURNING version`, job.RecordID).Scan(&version)
			} else {
				err = f.owner.QueryRow(f.ctx, `UPDATE fuel.capture_jobs SET state='running',attempts=5,lease_token=$2,
 lease_until=clock_timestamp()-interval '1 second',expires_at=clock_timestamp()+interval '1 hour',
 next_attempt_at=clock_timestamp()+interval '1 day',version=version+1 WHERE id=$1 RETURNING version`, job.RecordID, value.UUID()).Scan(&version)
			}
			if err != nil {
				t.Fatal("inject terminal lifecycle state", err)
			}
			counts := func() [6]int64 {
				t.Helper()
				var result [6]int64
				err := f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM fuel.module_operations),(SELECT count(*) FROM fuel.module_effects),
 (SELECT count(*) FROM fuel.fillups),(SELECT count(*) FROM fuel.odometer_observations),
 (SELECT count(*) FROM fuel.charging_sessions),(SELECT count(*) FROM fuel.trips)`).Scan(
					&result[0], &result[1], &result[2], &result[3], &result[4], &result[5])
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			before := counts()
			calls := 0
			extract := func(context.Context, store.FuelCaptureSource) (string, error) {
				calls++
				return "Kilometerstand: 1234 km", nil
			}
			if err := f.app.Store.FuelModuleCaptureHousekeeping(f.ctx, true, extract); err != nil {
				t.Fatal("terminal capture housekeeping", err)
			}
			var state, code, text, suggestions, warnings string
			var currentVersion, capabilities int64
			var clearedLease bool
			err = f.owner.QueryRow(f.ctx, `SELECT state,error_code,source_text,suggestions::text,warnings::text,version,
 lease_token IS NULL AND lease_until IS NULL,(SELECT count(*) FROM fuel.capture_transitions)
 FROM fuel.capture_jobs WHERE id=$1`, job.RecordID).Scan(&state, &code, &text, &suggestions, &warnings, &currentVersion, &clearedLease, &capabilities)
			if err != nil || calls != 0 || state != "expired" || code != "authority_unavailable" || text != "" || suggestions != "{}" || warnings != "[]" || currentVersion != version+1 || !clearedLease || capabilities != 0 {
				t.Fatal("terminal capture was extracted, stranded, or retained a capability", calls, state, code, text, suggestions, warnings, currentVersion, clearedLease, capabilities, err)
			}
			if after := counts(); after != before {
				t.Fatal("terminal proposal housekeeping created accepted intent or business facts", before, after)
			}
			if err := f.app.Store.FuelModuleCaptureHousekeeping(f.ctx, true, extract); err != nil {
				t.Fatal("repeat terminal housekeeping", err)
			}
			var repeatedVersion int64
			if err := f.pool.QueryRow(f.ctx, "SELECT version FROM fuel.capture_jobs WHERE id=$1", job.RecordID).Scan(&repeatedVersion); err != nil || calls != 0 || repeatedVersion != currentVersion {
				t.Fatal("terminal proposal was selected again", calls, repeatedVersion, err)
			}
			if err := database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal("terminal transition weakened module readiness", err)
			}
		})
	}
}
