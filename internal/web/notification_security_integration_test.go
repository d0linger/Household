//go:build integration

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/securityevent"
	"household/internal/value"

	"github.com/pquerna/otp/totp"
)

func personalSecurityCount(t *testing.T, f *integrationFixture, user, kind string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.notifications WHERE user_id=$1 AND kind=$2", user, "security_"+kind).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func personalSecurityExpect(t *testing.T, f *integrationFixture, user, kind string, want int) {
	t.Helper()
	if got := personalSecurityCount(t, f, user, kind); got != want {
		t.Fatalf("personal %s event count=%d want=%d", kind, got, want)
	}
	var malformed int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notifications n WHERE n.user_id=$1 AND n.kind=$2 AND
 (n.module_id IS NOT NULL OR n.title<>'Sicherheitsereignis' OR n.target_path<>'/settings/security' OR n.dedupe_key<>'security-event/'||n.id::text OR
 NOT EXISTS(SELECT 1 FROM core.jobs j WHERE j.household_id=n.household_id AND j.kind=$3 AND j.dedupe_key='security-event-job/'||n.id::text
 AND j.payload=jsonb_build_object('event_id',n.id::text) AND j.module_id IS NULL AND j.module_generation IS NULL))`, user, "security_"+kind, securityevent.JobKind).Scan(&malformed); err != nil || malformed != 0 {
		t.Fatal("security inbox and closed durable source contract", malformed, err)
	}
}

func TestNotificationSecurityActualAuthenticationHooksAndOwnInbox(t *testing.T) {
	f := newIntegrationFixture(t)
	user := f.identity.UserID
	passwordBefore := personalSecurityCount(t, f, user, "password_changed")
	if err := f.app.Auth.ChangePassword(f.ctx, f.identity, "wrong-password", "Replacement household password 2026!"); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("wrong password change", err)
	}
	personalSecurityExpect(t, f, user, "password_changed", passwordBefore)
	// Setup and failed confirmation are not completed factor changes.
	secret, _, err := f.app.Auth.SetupTOTP(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "totp_enabled", 0)
	if _, err = f.app.Auth.EnableTOTP(f.ctx, f.identity, "not-a-code"); err == nil {
		t.Fatal("invalid TOTP accepted")
	}
	personalSecurityExpect(t, f, user, "totp_enabled", 0)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, err := f.app.Auth.EnableTOTP(f.ctx, f.identity, code)
	if err != nil || len(codes) != 8 {
		t.Fatal("actual TOTP activation", err)
	}
	personalSecurityExpect(t, f, user, "totp_enabled", 1)
	if err = f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, codes[0]); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "recovery_code_used", 1)
	if err = f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, codes[0]); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("recovery replay accepted", err)
	}
	personalSecurityExpect(t, f, user, "recovery_code_used", 1)
	if _, err = f.app.Auth.RegenerateRecoveryCodes(f.ctx, f.identity); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "recovery_codes_regenerated", 1)
	if err = f.app.Auth.DisableTOTP(f.ctx, f.identity); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "totp_disabled", 1)
	if err = f.app.Auth.DisableTOTP(f.ctx, f.identity); err == nil {
		t.Fatal("inactive factor unexpectedly disabled again")
	}
	personalSecurityExpect(t, f, user, "totp_disabled", 1)
	device := newSecurityAuthenticator(t, user)
	begin, err := f.app.Auth.BeginPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "PRIVATE security key label")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "PRIVATE user agent", begin["challenge_id"].(string), device.register(t, securityChallenge(t, begin))); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "passkey_added", 1)
	keys, err := f.app.Auth.ListPasskeys(f.ctx, f.identity)
	if err != nil || len(keys) != 1 {
		t.Fatal("registered passkey", err)
	}
	if err = f.app.Auth.ChangePasskey(f.ctx, f.identity, keys[0].ID, "PRIVATE renamed key", false); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "passkey_added", 1)
	personalSecurityExpect(t, f, user, "passkey_removed", 0)
	if err = f.app.Auth.ChangePasskey(f.ctx, f.identity, keys[0].ID, "", true); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "passkey_removed", 1)
	if _, _, err = f.app.Auth.Login(f.ctx, "unknown-security-account", "wrong", "", "127.0.0.9", "PRIVATE failed login"); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("unknown login result", err)
	}
	_, other, err := f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.2", "PRIVATE other device")
	if err != nil {
		t.Fatal(err)
	}
	sessionsBefore := personalSecurityCount(t, f, user, "sessions_revoked")
	if err = f.app.Auth.RevokeSession(f.ctx, f.identity, other.SessionID, "session_revoked"); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "sessions_revoked", sessionsBefore+1)
	if err = f.app.Auth.RevokeSession(f.ctx, f.identity, other.SessionID, "session_revoked"); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Auth.RevokeOtherSessions(f.ctx, f.identity); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "sessions_revoked", sessionsBefore+1)
	_, other, err = f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.3", "logout device")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Auth.RevokeSession(f.ctx, other, other.SessionID, "logout"); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "sessions_revoked", sessionsBefore+1)
	// Every session is revoked by the actual password mutation. The inbox must
	// survive for the same account's subsequent legitimate sign-in.
	const nextPassword = "Replacement household password 2026!"
	if err = f.app.Auth.ChangePassword(f.ctx, f.identity, integrationPassword, nextPassword); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, user, "password_changed", passwordBefore+1)
	if _, err = f.app.Auth.Resolve(f.ctx, f.token, false); !errors.Is(err, auth.ErrSession) {
		t.Fatal("old session survived password change", err)
	}
	token, identity, err := f.app.Auth.Login(f.ctx, f.identity.Username, nextPassword, "", "127.0.0.4", "fresh login")
	if err != nil || identity.UserID != user {
		t.Fatal("fresh login failed", err)
	}
	var notice string
	if err = f.pool.QueryRow(f.ctx, "SELECT id::text FROM core.notifications WHERE user_id=$1 AND kind='security_password_changed' ORDER BY created_at DESC LIMIT 1", user).Scan(&notice); err != nil {
		t.Fatal(err)
	}
	r, body, err := f.request(http.MethodGet, "/notifications", token, "", "", nil, nil)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(body), notice) || !strings.Contains(string(body), "/settings/security") {
		t.Fatal("personal security inbox not visible after all-session revocation", err)
	}
	var leaked int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.notifications WHERE kind LIKE 'security_%' AND (title LIKE '%PRIVATE%' OR title LIKE '%Replacement%' OR user_id<>$1)", user).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("security event leaked details or broadcast", leaked, err)
	}
}

func TestNotificationSecurityAdminTargetNoopAndAtomicSource(t *testing.T) {
	f := newIntegrationFixture(t)
	target := shoppingTestMember(t, f, "security-target", "editor")
	post := func(role string) int {
		t.Helper()
		form := url.Values{"role": {role}, "disabled": {"false"}}
		r, _, err := f.request(http.MethodPost, "/settings/users/"+target.UserID, f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode
	}
	var before, after int64
	if err := f.pool.QueryRow(f.ctx, "SELECT auth_version FROM core.users WHERE id=$1", target.UserID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if status := post("editor"); status != 303 {
		t.Fatal("no-op administrator change", status)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT auth_version FROM core.users WHERE id=$1", target.UserID).Scan(&after); err != nil || before != after {
		t.Fatal("no-op changed authentication generation", err)
	}
	personalSecurityExpect(t, f, target.UserID, "account_changed", 0)
	if status := post("reader"); status != 303 {
		t.Fatal("actual administrator role change", status)
	}
	personalSecurityExpect(t, f, target.UserID, "account_changed", 1)
	personalSecurityExpect(t, f, f.identity.UserID, "account_changed", 0)
	if status := post("reader"); status != 303 {
		t.Fatal(status)
	}
	personalSecurityExpect(t, f, target.UserID, "account_changed", 1)
	if status := post("invalid"); status < 400 {
		t.Fatal("invalid role accepted")
	}
	personalSecurityExpect(t, f, target.UserID, "account_changed", 1)
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	tx, err := audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "UPDATE core.users SET display_name='PRIVATE rolled back' WHERE id=$1", target.UserID); err != nil {
		t.Fatal(err)
	}
	if err = securityevent.Record(ctx, tx, target.HouseholdID, target.UserID, "account_changed"); err != nil {
		t.Fatal(err)
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = tx.Rollback(cleanup); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, target.UserID, "account_changed", 1)
	var name string
	if err = f.pool.QueryRow(f.ctx, "SELECT display_name FROM core.users WHERE id=$1", target.UserID).Scan(&name); err != nil || name == "PRIVATE rolled back" {
		t.Fatal("rolled-back event mutation persisted", err)
	}
	var orphans int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.jobs j WHERE j.kind=$1 AND NOT EXISTS(SELECT 1 FROM core.notifications n WHERE n.id::text=j.payload->>'event_id')`, securityevent.JobKind).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatal("rolled-back source work survived", orphans, err)
	}
	// Validate a rejected kind before writes, using the same real transaction API.
	tx, err = audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err = securityevent.Record(ctx, tx, f.identity.HouseholdID, f.identity.UserID, "PRIVATE arbitrary text "+value.UUID()); err == nil {
		t.Fatal("arbitrary security kind accepted")
	}
	_ = tx.Rollback(cleanup)
}
