//go:build integration

package web

import (
	"errors"
	"testing"
	"time"

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
	if _, err = f.app.Auth.EnableTOTP(f.ctx, f.identity, code); err != nil {
		t.Fatal(err)
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
	const concurrent = 5
	start, results := make(chan struct{}), make(chan error, concurrent)
	for range concurrent {
		go func() {
			<-start
			results <- login("not-a-code")
		}()
	}
	close(start)
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
	if code, err = totp.GenerateCode(secret, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = login(code); !errors.Is(err, auth.ErrLimited) {
		t.Fatal("a correct code must wait for the exhausted budget", err)
	}
}
