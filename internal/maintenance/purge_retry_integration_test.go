//go:build integration

package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPurgeDeadlockRetryRollsBackActualPostgresTransaction(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	if _, err := f.owner.Exec(ctx, "CREATE TABLE core.purge_retry_test(participant integer,attempt integer, PRIMARY KEY(participant,attempt))"); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		participant, calls, victims int
		err                         error
	}
	result := make(chan outcome, 2)
	ready := make(chan struct{}, 2)
	cycle := make(chan struct{})
	joined := 0
	defer func() {
		cancel()
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		for joined < 2 {
			select {
			case <-result:
				joined++
			case <-timeout.C:
				t.Error("deadlock participants did not stop")
				return
			}
		}
	}()
	for participant := range 2 {
		go func() {
			out := outcome{participant: participant}
			out.err = retryPurgeDeadlock(ctx, func() error {
				out.calls++
				tx, err := f.owner.Begin(ctx)
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err = tx.Exec(ctx, "INSERT INTO core.purge_retry_test VALUES($1,$2)", participant, out.calls); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(726241991+participant)); err != nil {
					return err
				}
				if out.calls == 1 {
					ready <- struct{}{}
					select {
					case <-cycle:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(726241992-participant)); err != nil {
					var failure *pgconn.PgError
					if errors.As(err, &failure) && failure.Code == "40P01" {
						out.victims++
					}
					return err
				}
				return tx.Commit(ctx)
			})
			result <- out
		}()
	}
	// Both first transactions hold opposite locks before closing the cycle.
	// Either may be PostgreSQL's victim; both use the same production retry.
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(cycle)
	calls, victims := 0, 0
	for range 2 {
		var out outcome
		select {
		case out = <-result:
			joined++
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if out.err != nil {
			t.Fatal(out.err)
		}
		calls += out.calls
		victims += out.victims
		var count, attempt int
		if err := f.owner.QueryRow(ctx, "SELECT count(*),min(attempt) FROM core.purge_retry_test WHERE participant=$1", out.participant).Scan(&count, &attempt); err != nil || count != 1 || attempt != out.calls {
			t.Fatalf("rolled back writes survived: count=%d attempt=%d calls=%d err=%v", count, attempt, out.calls, err)
		}
	}
	if calls != 3 || victims != 1 {
		t.Fatalf("want one actual aborted transaction: calls=%d victims=%d", calls, victims)
	}
}
