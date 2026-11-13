package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"household/internal/store"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFailureReason(t *testing.T) {
	t.Parallel()
	panicErr := guarded(t.Context(), func(context.Context) error { panic("reading 1234.5 kWh") })
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"panic", panicErr, "panic"},
		{"deadline", fmt.Errorf("claim: %w", context.DeadlineExceeded), "deadline"},
		{"lease lost", fmt.Errorf("finish: %w", store.ErrConflict), "lease_lost"},
		{"lease refused", store.ErrForbidden, "lease_refused"},
		{"database unreachable", fmt.Errorf("claim: %w", &pgconn.ConnectError{}), "database_unreachable"},
		{"database error keeps only its code", &pgconn.PgError{Code: "23514", Message: "value 1234.5 violates check"}, "database_23514"},
		{"other", errors.New("https://ha.example/api?token=secret"), "unclassified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := failureReason(tc.err); got != tc.want {
				t.Fatalf("failureReason = %q, want %q", got, tc.want)
			}
		})
	}
}
