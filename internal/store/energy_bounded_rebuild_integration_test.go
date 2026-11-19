//go:build integration

package store

import (
	"testing"
	"time"
)

type energyDirtyState struct {
	from, through, cursor time.Time
	cursorID, phase       string
	cursorSet             bool
}

func energyDirty(t *testing.T, f *energyNative) energyDirtyState {
	t.Helper()
	var state energyDirtyState
	var cursor *time.Time
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT from_at,through_at,cursor_at,cursor_id,phase FROM meters.dirty_ranges WHERE register_id=$1`, f.register).
		Scan(&state.from, &state.through, &cursor, &state.cursorID, &state.phase); err != nil {
		t.Fatal("read dirty range", err)
	}
	if cursor != nil {
		state.cursor, state.cursorSet = *cursor, true
	}
	return state
}

func energySetDirty(t *testing.T, f *energyNative, from, through time.Time, cursor *time.Time, cursorID, phase string) {
	t.Helper()
	if _, err := f.f.owner.Exec(f.f.ctx, `DELETE FROM meters.dirty_ranges WHERE register_id=$1`, f.register); err != nil {
		t.Fatal(err)
	}
	if _, err := f.f.owner.Exec(f.f.ctx, `INSERT INTO meters.dirty_ranges(register_id,household_id,from_at,through_at,cursor_at,cursor_id,source_revision,phase,created_by)
 SELECT id,household_id,$2,$3,$4,$5,data_revision,$6,created_by FROM meters.registers WHERE id=$1`, f.register, from, through, cursor, cursorID, phase); err != nil {
		t.Fatal("seed dirty range", err)
	}
}

func energyInvalidate(t *testing.T, f *energyNative, from, through time.Time) {
	t.Helper()
	if _, err := f.f.owner.Exec(f.f.ctx, `SELECT meters.invalidate($1,$2,$3,$4,$5)`, f.f.identity.HouseholdID, f.register, from, through, f.f.identity.UserID); err != nil {
		t.Fatal("invalidate", err)
	}
}

// Regression: a late range no longer restarts completed projection work for the
// whole accumulated range.
func TestEnergyNativeLateInvalidationKeepsCompletedWork(t *testing.T) {
	energyLateInvalidationKeepsCompletedWork(t, newEnergyNative(t, "manual"))
}

// The household's zone is in summer time on 15 July: the provisional device
// mapping then starts two hours before a device installed at midnight UTC.
func TestEnergyNativeLateInvalidationKeepsCompletedWorkInSummerTime(t *testing.T) {
	start := time.Date(time.Now().Year()-1, time.July, 15, 0, 0, 0, 0, time.UTC)
	energyLateInvalidationKeepsCompletedWork(t, newEnergyNativeAt(t, "manual", start))
}

func energyLateInvalidationKeepsCompletedWork(t *testing.T, f *energyNative) {
	t.Helper()
	energyNativeProject(t, f.f)
	base := f.start
	at := func(hours int) time.Time { return base.Add(time.Duration(hours) * time.Hour) }

	// Aggregates phase: intervals are complete and hours before the cursor final.
	cursor := at(30)
	energySetDirty(t, f, at(0), at(48), &cursor, "", "aggregates")
	energyInvalidate(t, f, at(40), at(50))
	got := energyDirty(t, f)
	if got.phase != "intervals" || !got.from.Equal(at(30)) || !got.through.Equal(at(50)) || !got.cursorSet || !got.cursor.Equal(at(40)) || got.cursorID != "" {
		t.Fatalf("late range after the aggregate cursor restarted too much: %+v", got)
	}
	energySetDirty(t, f, at(0), at(48), &cursor, "", "aggregates")
	energyInvalidate(t, f, at(10), at(12))
	if got = energyDirty(t, f); !got.from.Equal(at(10)) || !got.cursor.Equal(at(10)) || !got.through.Equal(at(48)) {
		t.Fatalf("range before the aggregate cursor must rebuild from its own start: %+v", got)
	}

	// Intervals phase: a later range keeps the cursor and its point key.
	cursor = at(20)
	energySetDirty(t, f, at(0), at(48), &cursor, "2/r/point", "intervals")
	energyInvalidate(t, f, at(30), at(50))
	if got = energyDirty(t, f); !got.from.Equal(at(0)) || !got.cursor.Equal(at(20)) || got.cursorID != "2/r/point" || !got.through.Equal(at(50)) {
		t.Fatalf("later range moved the interval cursor: %+v", got)
	}
	// An earlier range resumes at its own start, not at the merged range start.
	energyInvalidate(t, f, at(5), at(6))
	if got = energyDirty(t, f); !got.from.Equal(at(0)) || !got.cursor.Equal(at(5)) || got.cursorID != "" {
		t.Fatalf("earlier range did not resume at its own start: %+v", got)
	}
	if _, err := f.f.owner.Exec(f.f.ctx, `DELETE FROM meters.dirty_ranges WHERE register_id=$1`, f.register); err != nil {
		t.Fatal(err)
	}

	// The partial restart produces exactly the aggregates of a full rebuild.
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, at(3), "10", "actual"))
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, at(20), "30", "actual"))
	energyNativeProject(t, f.f)
	halfway := at(12)
	energySetDirty(t, f, at(0), at(20), &halfway, "", "aggregates")
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, at(30), "45", "actual"))
	energyNativeProject(t, f.f)
	snapshot := func() string {
		var out string
		if err := f.f.owner.QueryRow(f.f.ctx, `SELECT coalesce(string_agg(bucket_kind||'@'||from_at::text||'='||coalesce(quantity::text,'-')||'/'||covered_seconds||'/'||estimated_seconds,';' ORDER BY bucket_kind,from_at),'')
 FROM meters.aggregates WHERE register_id=$1`, f.register).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	partial := snapshot()
	// The replaced provisional device mapping starts at midnight in the
	// household's zone and leaves empty hours before base (one in winter, two
	// in summer time), so the full rebuild starts at the partial snapshot's
	// first bucket: both then cover the same bucket domain on any fixture date.
	var first time.Time
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT min(from_at) FROM meters.aggregates WHERE register_id=$1 AND bucket_kind='hour'`, f.register).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first.After(base) || first.Before(base.Add(-24*time.Hour)) {
		t.Fatalf("partial snapshot starts at %s, want within the day before %s", first, base)
	}
	if _, err := f.f.owner.Exec(f.f.ctx, `DELETE FROM meters.aggregates WHERE register_id=$1`, f.register); err != nil {
		t.Fatal(err)
	}
	energyInvalidate(t, f, first, at(31))
	energyNativeProject(t, f.f)
	if full := snapshot(); partial == "" || partial != full {
		t.Fatalf("partial restart differs from a full rebuild:\npartial %s\nfull    %s", partial, full)
	}
	energyAssertRollups(t, f)
}

// energyAssertRollups checks the day and month rows independently of the Go
// bucket grouping: each starts at a local calendar boundary of the register's
// time zone, spans exactly one calendar day or month there, and equals the sum
// of the hour rows inside it; every hour row lies in exactly one day and one
// month row.
func energyAssertRollups(t *testing.T, f *energyNative) {
	t.Helper()
	var rollups, wrong, uncovered int
	if err := f.f.owner.QueryRow(f.f.ctx, `WITH z AS(SELECT timezone tz FROM meters.registers WHERE id=$1),
 a AS(SELECT * FROM meters.aggregates WHERE register_id=$1)
SELECT (SELECT count(*) FROM a WHERE bucket_kind IN('day','month')),
 (SELECT count(*) FROM a r,z WHERE r.bucket_kind IN('day','month') AND (
   date_trunc(r.bucket_kind,r.from_at AT TIME ZONE z.tz)<>r.from_at AT TIME ZONE z.tz
   OR r.through_at AT TIME ZONE z.tz<>r.from_at AT TIME ZONE z.tz+CASE r.bucket_kind WHEN 'day' THEN interval '1 day' ELSE interval '1 month' END
   OR r.quantity IS DISTINCT FROM (SELECT sum(h.quantity) FROM a h WHERE h.bucket_kind='hour' AND h.from_at>=r.from_at AND h.through_at<=r.through_at)
   OR r.covered_seconds<>(SELECT coalesce(sum(h.covered_seconds),0) FROM a h WHERE h.bucket_kind='hour' AND h.from_at>=r.from_at AND h.through_at<=r.through_at)
   OR r.estimated_seconds<>(SELECT coalesce(sum(h.estimated_seconds),0) FROM a h WHERE h.bucket_kind='hour' AND h.from_at>=r.from_at AND h.through_at<=r.through_at))),
 (SELECT count(*) FROM a h,unnest(ARRAY['day','month']) k WHERE h.bucket_kind='hour' AND
   (SELECT count(*) FROM a r WHERE r.bucket_kind=k AND h.from_at>=r.from_at AND h.through_at<=r.through_at)<>1)`, f.register).Scan(&rollups, &wrong, &uncovered); err != nil {
		t.Fatal("check rollups", err)
	}
	if rollups == 0 || wrong != 0 || uncovered != 0 {
		t.Fatalf("day/month rollups: %d rows, %d not a local calendar sum of their hours, %d hour placements not in exactly one day and month", rollups, wrong, uncovered)
	}
}

// Regression: a long historical aggregate rebuild continues at a paced rate;
// corrections within the last year continue at once.
func TestEnergyNativeLongAggregateRebuildIsPaced(t *testing.T) {
	f := newEnergyNative(t, "manual")
	energyNativeProject(t, f.f)
	now := time.Now().UTC().Truncate(time.Hour)
	for _, tc := range []struct {
		name  string
		from  time.Time
		paced bool
	}{
		{"historical backlog", now.AddDate(-2, 0, 0), true},
		{"recent correction", now.AddDate(0, -3, 0), false},
		{"short backlog", now.Add(-60 * time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := tc.from
			energySetDirty(t, f, tc.from, now, &cursor, "", "aggregates")
			job, lease := energyNativeLease(t, f.f)
			// The delay is checked against database times taken around the
			// completion, so a slow assertion afterwards cannot change the result.
			var before, after, available time.Time
			if err := f.f.owner.QueryRow(f.f.ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := f.f.store.CompleteEnergyWorkspaceJob(f.f.ctx, job, lease); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := f.f.owner.QueryRow(f.f.ctx, `SELECT clock_timestamp(),state,available_at FROM core.jobs WHERE id=$1`, job).Scan(&after, &state, &available); err != nil {
				t.Fatal(err)
			}
			delay := time.Duration(0)
			if tc.paced {
				delay = energyAggregatePace
			}
			if state != "pending" || available.Before(before.Add(delay)) || available.After(after.Add(delay)) {
				t.Fatalf("state %s available %s, want pending and available within [%s, %s]", state, available, before.Add(delay), after.Add(delay))
			}
			if got := energyDirty(t, f); !got.cursor.Equal(tc.from.Add(energyAggregateBatchHours * time.Hour)) {
				t.Fatalf("one batch must advance exactly %d hours: %+v", energyAggregateBatchHours, got)
			}
			if _, err := f.f.owner.Exec(f.f.ctx, `DELETE FROM core.jobs WHERE id=$1`, job); err != nil {
				t.Fatal(err)
			}
		})
	}
}
