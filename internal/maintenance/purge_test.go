package maintenance

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPurgeRejectsInvalidRequestBeforeTransaction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, request string }{
		{"missing", ""},
		{"module name", "shopping"},
		{"SQL text", "'; TRUNCATE core.users; --"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := Purge(t.Context(), nil, tc.request); err == nil {
				t.Fatal("invalid purge request accepted")
			}
		})
	}
}

func TestPurgeDeadlockRetryBoundaries(t *testing.T) {
	t.Parallel()
	deadlock := &pgconn.PgError{Code: "40P01"}
	for _, tc := range []struct {
		name      string
		failures  []error
		wantCalls int
		want      error
	}{
		{"success", []error{nil}, 1, nil},
		{"rolled back deadlock", []error{fmt.Errorf("statement: %w", deadlock), nil}, 2, nil},
		{"exhausted", []error{deadlock, deadlock, deadlock}, 3, deadlock},
		{"serialization", []error{&pgconn.PgError{Code: "40001"}}, 1, nil},
		{"lock timeout", []error{&pgconn.PgError{Code: "55P03"}}, 1, nil},
		{"ambiguous commit", []error{errors.New("commit connection lost")}, 1, nil},
		{"authority revoked after rollback", []error{deadlock, errors.New("request invalid")}, 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			got := retryPurgeDeadlock(t.Context(), func() error { calls++; return tc.failures[calls-1] })
			want := tc.want
			if want == nil {
				want = tc.failures[len(tc.failures)-1]
			}
			if calls != tc.wantCalls || !errors.Is(got, want) {
				t.Fatalf("calls=%d error=%v, want %d %v", calls, got, tc.wantCalls, want)
			}
		})
	}
}

func TestPurgeDeadlockRetryCancellation(t *testing.T) {
	t.Parallel()
	for _, before := range []bool{true, false} {
		ctx, cancel := context.WithCancel(t.Context())
		if before {
			cancel()
		}
		calls := 0
		err := retryPurgeDeadlock(ctx, func() error { calls++; cancel(); return &pgconn.PgError{Code: "40P01"} })
		cancel()
		want := 1
		if before {
			want = 0
		}
		if !errors.Is(err, context.Canceled) || calls != want {
			t.Fatalf("calls=%d error=%v", calls, err)
		}
	}
}
