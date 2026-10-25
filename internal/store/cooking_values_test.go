package store

import (
	"testing"
	"time"
)

func TestCookingQuantity(t *testing.T) {
	for _, tc := range []struct {
		name, amount, base, servings, display, exact string
		approximate                                  bool
	}{
		{"exact large", "9007199254.740993", "2", "4", "18014398509.481986", "9007199254740993/500000", false},
		{"third", "1", "3", "1", "0.333333", "1/3", true},
		{"tiny positive", "0.000001", "1000000", "0.000001", "<0.000001", "1/1000000000000000000", true},
		{"beyond input bound", "999999999999", "0.000001", "999999999999", "999999999998000000000001000000", "999999999998000000000001000000", false},
		{"comma", "1,25", "2,5", "5", "2.5", "5/2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := cookingServings(tc.base)
			if err != nil {
				t.Fatal(err)
			}
			servings, err := cookingServings(tc.servings)
			if err != nil {
				t.Fatal(err)
			}
			got, err := cookingQuantity(tc.amount, base, servings)
			if err != nil || got.Display != tc.display || got.Exact != tc.exact || got.Approximate != tc.approximate {
				t.Fatalf("got %#v, %v", got, err)
			}
			unknown, err := cookingQuantity(nil, base, servings)
			if err != nil || unknown != nil {
				t.Fatal("unknown quantity became known", unknown, err)
			}
		})
	}
	for _, raw := range []string{"0", "-1", "0.0000001", "1e3", "NaN", "1000000000000"} {
		t.Run("invalid "+raw, func(t *testing.T) {
			if _, err := cookingServings(raw); err == nil {
				t.Fatal("invalid servings accepted")
			}
		})
	}
}

func TestCookingTimerTransitions(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 59, 59, 0, time.UTC)
	timer, err := cookingTimerTransition(CookingTimer{ID: "timer", Name: "Ruhezeit"}, "restart", 90, now)
	if err != nil || timer.Version != 1 || cookingTimerRemaining(timer, now) != 90000 {
		t.Fatal(timer, err)
	}
	paused, err := cookingTimerTransition(timer, "pause", 0, now.Add(12500*time.Millisecond))
	if err != nil || paused.Deadline != nil || cookingTimerRemaining(paused, now.Add(time.Hour)) != 77500 {
		t.Fatal(paused, err)
	}
	resumed, err := cookingTimerTransition(paused, "resume", 0, now.Add(time.Hour))
	if err != nil || resumed.Version != 3 || cookingTimerRemaining(resumed, now.Add(time.Hour)) != 77500 {
		t.Fatal(resumed, err)
	}
	if _, err = cookingTimerTransition(resumed, "pause", 0, *resumed.Deadline); err == nil {
		t.Fatal("elapsed timer resurrected by pausing")
	}
	done, err := cookingTimerTransition(resumed, "acknowledge", 0, resumed.Deadline.Add(time.Hour))
	if err != nil || done.State != "done" || done.Deadline != nil || done.RemainingMillis != nil {
		t.Fatal(done, err)
	}
	for _, tc := range []struct {
		name, action string
		seconds      int64
		timer        CookingTimer
	}{
		{"zero", "restart", 0, timer}, {"over day", "restart", 86401, timer},
		{"unknown", "execute", 30, timer}, {"resume running", "resume", 0, timer},
		{"pause paused", "pause", 0, paused}, {"resume done", "resume", 0, done},
		{"early acknowledge", "acknowledge", 0, timer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cookingTimerTransition(tc.timer, tc.action, tc.seconds, now); err == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
}
