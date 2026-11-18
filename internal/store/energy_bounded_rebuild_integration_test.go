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
	f := newEnergyNative(t, "manual")
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
	if _, err := f.f.owner.Exec(f.f.ctx, `DELETE FROM meters.aggregates WHERE register_id=$1`, f.register); err != nil {
		t.Fatal(err)
	}
	energyInvalidate(t, f, base.Add(-time.Hour), at(31))
	energyNativeProject(t, f.f)
	if full := snapshot(); partial == "" || partial != full {
		t.Fatalf("partial restart differs from a full rebuild:\npartial %s\nfull    %s", partial, full)
	}
}

// Regression: a long historical aggregate rebuild continues at a paced rate;
// a short one continues at once.
func TestEnergyNativeLongAggregateRebuildIsPaced(t *testing.T) {
	f := newEnergyNative(t, "manual")
	energyNativeProject(t, f.f)
	now := time.Now().UTC().Truncate(time.Hour)
	for _, tc := range []struct {
		name  string
		from  time.Time
		paced bool
	}{
		{"long backlog", now.AddDate(0, 0, -30), true},
		{"short backlog", now.Add(-60 * time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := tc.from
			energySetDirty(t, f, tc.from, now, &cursor, "", "aggregates")
			job, lease := energyNativeLease(t, f.f)
			if err := f.f.store.CompleteEnergyWorkspaceJob(f.f.ctx, job, lease); err != nil {
				t.Fatal(err)
			}
			var state string
			var delayed bool
			if err := f.f.owner.QueryRow(f.f.ctx, `SELECT state,available_at>clock_timestamp()+interval '3 seconds' FROM core.jobs WHERE id=$1`, job).Scan(&state, &delayed); err != nil {
				t.Fatal(err)
			}
			if state != "pending" || delayed != tc.paced {
				t.Fatalf("state %s delayed %v, want pending and delayed=%v", state, delayed, tc.paced)
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
