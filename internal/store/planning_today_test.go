package store

import (
	"context"
	"errors"
	"testing"

	"household/internal/auth"
)

func TestTodayMealsRejectsUnboundedOrInvalidDayBeforeDatabase(t *testing.T) {
	t.Parallel()
	s := &Store{}
	for _, tc := range []struct {
		day   string
		limit int
	}{{"", 7}, {"2026-02-30", 7}, {"2026-11-11", 0}, {"2026-11-11", 13}, {"2026-11-11", -1}} {
		if _, err := s.TodayMeals(context.Background(), auth.Identity{}, tc.day, tc.limit); !errors.Is(err, ErrNotFound) {
			t.Fatal("invalid projection request reached database", tc, err)
		}
	}
}
