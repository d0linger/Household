package store

import (
	"strings"
	"testing"
	"time"

	"household/internal/notification"
)

func TestNotificationMealplanCivilTimeAndExplicitSteps(t *testing.T) {
	for _, tc := range []struct{ date, clock, zone, want string }{
		{"2026-10-25", "02:30", "Europe/Vienna", "2026-10-25T00:30:00Z"},
		{"2028-02-29", "09:15", "Europe/Vienna", "2028-02-29T08:15:00Z"},
		{"2026-03-29", "02:30", "Europe/Vienna", ""},
		{"2026-02-29", "09:00", "UTC", ""},
		{"2026-01-01", "24:00", "UTC", ""},
		{"2026-01-01", "12:00", "Invalid/Zone", ""},
	} {
		t.Run(tc.date+tc.clock+tc.zone, func(t *testing.T) {
			at, err := planningNotificationTime(tc.date, tc.clock, tc.zone)
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid local date/time accepted", at)
				}
			} else if err != nil || at.Format(time.RFC3339) != tc.want {
				t.Fatal("explicit calendar policy", at, err)
			}
		})
	}
	base := PlanningNotificationInput{Confirmed: true, Enabled: true, Timezone: "UTC", Responsible: "00000000-0000-4000-8000-000000000001",
		Steps: []PlanningNotificationStepInput{{ID: "00000000-0000-4000-8000-000000000002", Kind: "start", Label: "Reviewed preparation", LocalDate: "2028-02-29", LocalTime: "12:00"}}}
	if steps, err := planningNotificationSteps(base); err != nil || len(steps) != 1 {
		t.Fatal(steps, err)
	}
	for name, change := range map[string]func(*PlanningNotificationInput){
		"unconfirmed":    func(v *PlanningNotificationInput) { v.Confirmed = false },
		"unassigned":     func(v *PlanningNotificationInput) { v.Responsible = "" },
		"empty":          func(v *PlanningNotificationInput) { v.Steps = nil },
		"zero recipient": func(v *PlanningNotificationInput) { v.Responsible = "00000000-0000-0000-0000-000000000000" },
		"zero step":      func(v *PlanningNotificationInput) { v.Steps[0].ID = "00000000-0000-0000-0000-000000000000" },
		"too many": func(v *PlanningNotificationInput) {
			v.Steps = make([]PlanningNotificationStepInput, 9)
		},
		"duplicate":  func(v *PlanningNotificationInput) { v.Steps = append(v.Steps, v.Steps[0]) },
		"kind":       func(v *PlanningNotificationInput) { v.Steps[0].Kind = "timer" },
		"long label": func(v *PlanningNotificationInput) { v.Steps[0].Label = strings.Repeat("ü", 121) },
		"control":    func(v *PlanningNotificationInput) { v.Steps[0].Label = "step\nsecret" },
		"second start": func(v *PlanningNotificationInput) {
			v.Steps = append(v.Steps, PlanningNotificationStepInput{ID: "00000000-0000-4000-8000-000000000003", Kind: "start", Label: "Other", LocalDate: "2028-02-29", LocalTime: "13:00"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			in.Steps = append([]PlanningNotificationStepInput(nil), base.Steps...)
			change(&in)
			if _, err := planningNotificationSteps(in); err == nil {
				t.Fatal("invalid confirmed steps accepted")
			}
		})
	}
}

func TestNotificationMealplanCompiledTarget(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	i := notification.Intent{Schema: 1, HouseholdID: id, UserID: id, Module: "mealplan", Generation: 1, ResourceType: "mealplan_preparation", ResourceID: id,
		Revision: 1, Category: "mealplan", Occurrence: "step/" + id + "/" + id + "/2026-11-10", EventAt: at, NotBefore: at, ExpiresAt: at.Add(4 * time.Hour)}
	if got := NotificationTarget(i); got != "/m/mealplan/workspace/"+id+"?week=2026-11-10" {
		t.Fatal(got)
	}
	for _, occurrence := range []string{"step/../../settings", "step/" + id + "/" + id + "/2026-02-30", "step/" + id + "/" + id + "/2026-11-10/extra"} {
		i.Occurrence = occurrence
		if got := NotificationTarget(i); got != "" {
			t.Fatal("uncompiled destination", got)
		}
	}
}
