//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"household/internal/notification"
	"household/internal/value"
)

func TestNotificationCookingAtomicLifecycleConsentAndAuthority(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	recipe, err := f.store.SaveRecipe(f.ctx, f.identity, "", value.UUID(), value.UUID(), RecipeInput{
		Generation: f.store.States()["recipes"].Generation,
		Data:       map[string]json.RawMessage{"title": json.RawMessage(`"Private timer source"`), "base_servings": json.RawMessage(`"2"`)},
		Steps:      []RecipeRow{{Key: value.UUID(), Data: map[string]json.RawMessage{"instruction": json.RawMessage(`"Wait privately"`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.store.CookingSource(f.ctx, f.identity, String(recipe["id"]))
	if err != nil {
		t.Fatal(err)
	}
	start := func(t *testing.T) CookingSession {
		t.Helper()
		r, e := f.store.MutateCooking(f.ctx, f.identity, "", value.UUID(), value.UUID(), CookingInput{Action: "start", Generation: f.store.States()["recipes"].Generation, SourceVersionID: String(source["source_version_id"]), Servings: "2"})
		if e != nil {
			t.Fatal(e)
		}
		s, e := f.store.CookingSession(f.ctx, f.identity, String(r["id"]))
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	change := func(t *testing.T, s CookingSession, in CookingInput) CookingSession {
		t.Helper()
		in.Generation, in.Version = f.store.States()["recipes"].Generation, s.Version
		if _, e := f.store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), in); e != nil {
			t.Fatal(e)
		}
		s, e := f.store.CookingSession(f.ctx, f.identity, s.ID)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	pending := func(t *testing.T, timer string) []string {
		t.Helper()
		return notificationChorePending(t, f, timer, f.identity.UserID, "cooking_timer")
	}
	type timerIntent struct {
		notification.Intent
		ID string
	}
	one := func(t *testing.T, s CookingSession) timerIntent {
		t.Helper()
		ids := pending(t, s.Timers[0].ID)
		if len(ids) != 1 {
			t.Fatalf("wanted one current timer intent, got %d", len(ids))
		}
		return timerIntent{Intent: notificationChoreAudience(t, f, ids[0], true), ID: ids[0]}
	}
	t.Run("consent at action time never backfills an existing timer", func(t *testing.T) {
		s := change(t, start(t), CookingInput{Action: "timer_add", TimerName: "No prior consent", Seconds: 120})
		if len(pending(t, s.Timers[0].ID)) != 0 {
			t.Fatal("timer sent without consent")
		}
		notificationChoreConsent(t, f, f.identity, "cooking_timer", false)
		s = change(t, s, CookingInput{Action: "progress", CurrentStep: 0})
		if len(pending(t, s.Timers[0].ID)) != 0 {
			t.Fatal("late consent replayed timer")
		}
		s = change(t, s, CookingInput{Action: "restart", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version, Seconds: 120})
		i := one(t, s)
		if i.Revision != s.Timers[0].Version || !i.NotBefore.Equal(*s.Timers[0].Deadline) || !i.ExpiresAt.Equal(i.NotBefore.Add(time.Minute)) || !i.EventAt.Before(i.NotBefore) {
			t.Fatal("timer action/deadline/revision contract differs from persisted source")
		}
		change(t, s, CookingInput{Action: "abandon"})
	})
	t.Run("progress preserves timer while failed restart rolls back cancellation", func(t *testing.T) {
		s := change(t, start(t), CookingInput{Action: "timer_add", TimerName: "Private timer", Seconds: 120})
		i := one(t, s)
		for _, in := range []CookingInput{{Action: "progress", CurrentStep: 0}, {Action: "portions", Servings: "3"}} {
			s = change(t, s, in)
			if one(t, s).ID != i.ID {
				t.Fatal("unrelated session progress replaced timer intent")
			}
		}
		for _, in := range []CookingInput{
			{Action: "restart", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version, Seconds: 0},
			{Action: "restart", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version + 1, Seconds: 120},
		} {
			in.Generation, in.Version = f.store.States()["recipes"].Generation, s.Version
			request := value.UUID()
			if _, e := f.store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), request, in); e == nil {
				t.Fatal("invalid restart succeeded")
			}
			after, e := f.store.CookingSession(f.ctx, f.identity, s.ID)
			if e != nil || after.Version != s.Version || after.Timers[0].Version != s.Timers[0].Version || one(t, after).ID != i.ID {
				t.Fatal("failed mutation changed timer/intent", e)
			}
			var audits int
			if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE request_id=$1`, request).Scan(&audits); e != nil || audits != 0 {
				t.Fatal("failed timer mutation left audit", audits, e)
			}
		}
		s = change(t, s, CookingInput{Action: "pause", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version})
		if len(pending(t, s.Timers[0].ID)) != 0 {
			t.Fatal("pause retained pending alarm")
		}
		notificationChoreAudience(t, f, i.ID, false)
		s = change(t, s, CookingInput{Action: "resume", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version})
		resumed := one(t, s)
		if resumed.ID == i.ID || resumed.Revision <= i.Revision {
			t.Fatal("resume reused superseded alarm")
		}
		in := CookingInput{Action: "restart", Generation: f.store.States()["recipes"].Generation, Version: s.Version, TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version, Seconds: 90}
		key := value.UUID()
		for range 2 {
			if _, e := f.store.MutateCooking(f.ctx, f.identity, s.ID, key, value.UUID(), in); e != nil {
				t.Fatal("restart/replay", e)
			}
		}
		s, err = f.store.CookingSession(f.ctx, f.identity, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if one(t, s).ID == resumed.ID {
			t.Fatal("restart retained old deadline")
		}
		notificationChoreAudience(t, f, resumed.ID, false)
		change(t, s, CookingInput{Action: "abandon"})
	})
	for _, action := range []string{"timer_remove", "finish", "abandon"} {
		t.Run(action+" cancels all relevant alarms atomically", func(t *testing.T) {
			s := change(t, start(t), CookingInput{Action: "timer_add", TimerName: "Terminal timer", Seconds: 120})
			i := one(t, s)
			in := CookingInput{Action: action, TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version, ActualServings: "2", CookedOn: s.Today}
			change(t, s, in)
			if len(pending(t, i.ResourceID)) != 0 {
				t.Fatal("terminal action retained pending alarm")
			}
			notificationChoreAudience(t, f, i.ID, false)
		})
	}
	t.Run("acknowledge expired timer consumes only its alarm", func(t *testing.T) {
		s := change(t, start(t), CookingInput{Action: "timer_add", TimerName: "Short timer", Seconds: 1})
		i := one(t, s)
		timer := time.NewTimer(time.Until(*s.Timers[0].Deadline) + 20*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
		s = change(t, s, CookingInput{Action: "acknowledge", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version})
		if s.Timers[0].State != "done" || len(pending(t, i.ResourceID)) != 0 {
			t.Fatal("acknowledged alarm survived")
		}
		change(t, s, CookingInput{Action: "abandon"})
	})
	t.Run("owner login source revision and module generation are fenced", func(t *testing.T) {
		s := change(t, start(t), CookingInput{Action: "timer_add", TimerName: "Owned timer", Seconds: 120})
		i := one(t, s)
		other := financeAuthorityMember(t, f, "editor")
		if _, e := f.store.CookingTimerSession(f.ctx, other, i.ResourceID); !errors.Is(e, ErrNotFound) {
			t.Fatal("another member resolved private timer", e)
		}
		if session, e := f.store.CookingTimerSession(f.ctx, f.identity, i.ResourceID); e != nil || session != s.ID {
			t.Fatal("owner cannot resolve timer", e)
		}
		tx, e := f.app.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer rollback(tx)
		for _, mutate := range []func(*notification.Intent){func(v *notification.Intent) { v.UserID = other.UserID }, func(v *notification.Intent) { v.Revision++ }, func(v *notification.Intent) { v.Generation++ }, func(v *notification.Intent) { v.NotBefore = v.NotBefore.Add(time.Second) }} {
			changed := i.Intent
			mutate(&changed)
			if allowed, e := f.store.NotificationAudience(f.ctx, tx, changed); e != nil || allowed {
				t.Fatal("altered timer authority accepted", e)
			}
		}
		if e = tx.Rollback(f.ctx); e != nil {
			t.Fatal(e)
		}
		if _, e = f.owner.Exec(f.ctx, `UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1`, f.identity.SessionID); e != nil {
			t.Fatal(e)
		}
		if _, e = f.store.CookingTimerSession(f.ctx, f.identity, i.ResourceID); !errors.Is(e, ErrNotFound) {
			t.Fatal("revoked login resolved timer", e)
		}
	})
}
