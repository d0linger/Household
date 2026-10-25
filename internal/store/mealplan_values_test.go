package store

import "testing"

func TestPlanningCalendarWeekAndCopy(t *testing.T) {
	for _, tc := range []struct{ name, start, end string }{
		{"spring-DST", "2026-03-25", "2026-03-31"}, {"autumn-DST", "2026-10-22", "2026-10-28"}, {"year-boundary", "2026-12-29", "2027-01-04"}, {"leap-day", "2028-02-27", "2028-03-04"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			days, err := planningWindow(tc.start)
			if err != nil || len(days) != 7 || days[6] != tc.end {
				t.Fatal(days, err)
			}
			copied, err := planningCopyDate(days[6], tc.start, "2027-01-01")
			if err != nil || copied != "2027-01-07" {
				t.Fatal(copied, err)
			}
		})
	}
	for _, raw := range []string{"2026-02-30", "2200-12-29", "1899-12-31", ""} {
		t.Run("reject-"+raw, func(t *testing.T) {
			if _, err := planningWindow(raw); err == nil {
				t.Fatal("invalid window accepted")
			}
		})
	}
	if _, err := planningCopyDate("2026-10-29", "2026-10-22", "2026-11-01"); err == nil {
		t.Fatal("copied a meal outside selected week")
	}
}

func TestPlanningEffortUnknownAndConfirmedZero(t *testing.T) {
	zero, prep, cook := int64(0), int64(10), int64(20)
	var effort PlanningEffort
	effort.add(&prep, &cook)
	effort.add(nil, &zero)
	effort.add(&zero, nil)
	if effort.PrepMinutes != 10 || effort.CookMinutes != 20 || effort.UnknownPrep != 1 || effort.UnknownCook != 1 {
		t.Fatal(effort)
	}
}

func TestPlanningExactShoppingQuantity(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		raw                  any
		base, servings, want string
		valid                bool
	}{
		{"beyond-js-integer", "9007199254.740993", "2", "4", "18014398509.481986", true},
		{"fraction-review", "1", "3", "1", "", false},
		{"tiny-positive-review", "0.000001", "2", "1", "", false},
		{"overflow-review", "999999999999", "1", "2", "", false},
		{"unknown", nil, "2", "4", "", true},
		{"zero-base", "1", "0", "2", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planningQuantity(tc.raw, tc.base, tc.servings)
			if (err == nil) != tc.valid {
				t.Fatal(got, err)
			}
			if tc.valid && tc.raw != nil && got != tc.want {
				t.Fatal(got)
			}
			if tc.raw == nil && got != nil {
				t.Fatal("unknown became known")
			}
		})
	}
}
