//go:build integration

package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"

	"github.com/pquerna/otp/totp"
)

// Regression: the second-factor failure budget holds under concurrent wrong
// codes. With nine failures recorded, exactly one more wrong code is checked;
// every concurrent request after it is refused before verification.
func TestSecondFactorBudgetHoldsUnderConcurrentWrongCodes(t *testing.T) {
	f := newIntegrationFixture(t)
	secret, _, err := f.app.Auth.SetupTOTP(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := f.app.Auth.EnableTOTP(f.ctx, f.identity, code)
	if err != nil || len(recovery) < 2 {
		t.Fatal("enable second factor", err)
	}
	// The budget is a 15-minute database window. Start well inside one, so
	// the nine failures and the concurrent attempts share it.
	var into float64
	if err = f.owner.QueryRow(f.ctx, `SELECT extract(epoch FROM clock_timestamp())::numeric%900`).Scan(&into); err != nil {
		t.Fatal(err)
	}
	if left := time.Duration((900 - into) * float64(time.Second)); left < 2*time.Minute {
		time.Sleep(left + time.Second)
	}
	const ip = "198.51.100.7"
	login := func(factor string) error {
		_, _, err := f.app.Auth.Login(f.ctx, "bootstrap", integrationPassword, factor, ip, "factor-budget")
		return err
	}
	for attempt := range 9 {
		if err = login("not-a-code"); !errors.Is(err, auth.ErrCredentials) {
			t.Fatal("wrong code", attempt, err)
		}
	}
	// Hold the second factor so the concurrent requests demonstrably queue at
	// factor admission before any of them is decided.
	hold, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hold.Rollback(ctx)
	}()
	if _, err = hold.Exec(f.ctx, `SELECT 1 FROM core.totp_credentials WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE`, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	const concurrent = 5
	results := make(chan error, concurrent)
	for range concurrent {
		go func() { results <- login("not-a-code") }()
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		// pg_locks is visible to every role (pg_stat_activity hides other roles' waits);
		// row-lock waits carry no database OID, and this test server is disposable.
		var waiting int
		if err = f.owner.QueryRow(f.ctx, `SELECT count(DISTINCT pid) FROM pg_locks WHERE NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent logins never contended at factor admission", waiting)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err = hold.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	checked, limited := 0, 0
	for range concurrent {
		switch err := <-results; {
		case errors.Is(err, auth.ErrCredentials):
			checked++
		case errors.Is(err, auth.ErrLimited):
			limited++
		default:
			t.Fatal("unexpected concurrent result", err)
		}
	}
	if checked != 1 || limited != concurrent-1 {
		t.Fatalf("checked %d and limited %d wrong codes after nine failures; want 1 and %d", checked, limited, concurrent-1)
	}
	// An unused recovery code would otherwise succeed; the exhausted budget
	// must refuse it before verification.
	if err = login(recovery[1]); !errors.Is(err, auth.ErrLimited) {
		t.Fatal("a valid factor must wait for the exhausted budget", err)
	}
}

// Regression: Reauthenticate reads the failure budget only after it holds the
// TOTP row lock. While it waits on that lock, the budget is exhausted
// elsewhere; once released, it must see the exhausted budget and refuse
// before verifying. After a factor reset, earlier failures no longer refuse a
// correct password.
func TestSecondFactorAdmissionWaitsOnTheTOTPLockAndResetClearsTheBudget(t *testing.T) {
	f := newIntegrationFixture(t)
	secret, _, err := f.app.Auth.SetupTOTP(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Auth.EnableTOTP(f.ctx, f.identity, code); err != nil {
		t.Fatal(err)
	}
	var into float64
	if err = f.owner.QueryRow(f.ctx, `SELECT extract(epoch FROM clock_timestamp())::numeric%900`).Scan(&into); err != nil {
		t.Fatal(err)
	}
	if left := time.Duration((900 - into) * float64(time.Second)); left < 2*time.Minute {
		time.Sleep(left + time.Second)
	}
	hold, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hold.Rollback(ctx)
	}()
	var holder string
	if err = hold.QueryRow(f.ctx, `SELECT pg_current_xact_id()::xid::text FROM core.totp_credentials WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE`, f.identity.UserID).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, "not-a-code") }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		// The reauthentication waits on exactly the held TOTP row's transaction.
		var waiting bool
		if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND locktype='transactionid' AND transactionid::text=$1)`, holder).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reauthentication never waited on the held TOTP row")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for range 10 {
		_ = f.app.Auth.Limit(f.ctx, "totp-failure", f.identity.UserID, 10, 15*time.Minute)
	}
	if err = hold.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, auth.ErrLimited) {
		t.Fatal("the budget must be read after the TOTP lock, not before it", err)
	}
	// A factor reset removes the credential; earlier failures no longer count.
	reset, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reset.Exec(f.ctx, `DELETE FROM core.totp_credentials WHERE user_id=$1`, f.identity.UserID); err != nil {
		_ = reset.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = reset.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.app.Auth.Login(f.ctx, "bootstrap", integrationPassword, "", "198.51.100.9", "factor-reset"); err != nil {
		t.Fatal("a correct password must work after a factor reset", err)
	}
}
