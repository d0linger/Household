//go:build integration

package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/auth"

	"github.com/pquerna/otp/totp"
)

func TestSecurityRecentAuthenticationAndAdminCreation(t *testing.T) {
	f := newIntegrationFixture(t)
	var expiry time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT absolute_expires_at FROM core.sessions WHERE id=$1", f.identity.SessionID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET created_at=now()-interval '1 hour',reauthenticated_at=now()-interval '1 hour' WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	post := func(path string, form url.Values) (*integrationResponse, []byte) {
		t.Helper()
		r, b, err := f.request(http.MethodPost, path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), map[string]string{"Accept": "application/json"})
		if err != nil {
			t.Fatal(err)
		}
		return r, b
	}
	form := url.Values{"username": {"newmember"}, "display_name": {"New member"}, "role": {"editor"}, "password": {integrationPassword}}
	response, body := post("/settings/users", form)
	if response.StatusCode != 422 || !strings.Contains(string(body), "reauth_required") {
		t.Fatalf("old administrator got misleading refusal: %d %s", response.StatusCode, body)
	}
	response, _ = post("/settings/security/reauth", url.Values{"password": {"wrong"}})
	if response.StatusCode != 401 {
		t.Fatal("wrong reauthentication password accepted", response.StatusCode)
	}
	response, body = post("/settings/security/reauth", url.Values{"password": {integrationPassword}})
	if response.StatusCode != 200 {
		t.Fatalf("confirm=%d %s", response.StatusCode, body)
	}
	resolved, err := f.app.Auth.Resolve(f.ctx, f.token, false)
	if err != nil || !resolved.Recent() {
		t.Fatal("reauthentication not reflected", err)
	}
	var after time.Time
	if err = f.owner.QueryRow(f.ctx, "SELECT absolute_expires_at FROM core.sessions WHERE id=$1", f.identity.SessionID).Scan(&after); err != nil || !after.Equal(expiry) {
		t.Fatal("reauthentication extended absolute lifetime", err)
	}
	response, body = post("/settings/users", form)
	if response.StatusCode != 303 {
		t.Fatalf("confirmed admin cannot create member: %d %s", response.StatusCode, body)
	}
	var force bool
	var role string
	if err = f.owner.QueryRow(f.ctx, "SELECT u.must_change_password,m.role FROM core.users u JOIN core.memberships m ON m.user_id=u.id WHERE u.username='newmember'").Scan(&force, &role); err != nil || !force || role != "editor" {
		t.Fatal("new member contract", force, role, err)
	}
	reader := shoppingTestMember(t, f, "securityreader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.3", "Reader")
	if err != nil {
		t.Fatal(err)
	}
	r, b, err := f.request(http.MethodPost, "/settings/users", readerToken, f.app.Auth.CSRF(readerToken, "session"), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil)
	if err != nil || r.StatusCode != 403 {
		t.Fatalf("reader gained administrator permission: %v %v %s", r, err, b)
	}
}

func TestSecurityFactorRecoveryAndSessionLifecycle(t *testing.T) {
	f := newIntegrationFixture(t)
	other, _, err := f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.2", "Other device")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := f.app.Auth.SetupTOTP(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, err := f.app.Auth.EnableTOTP(f.ctx, f.identity, code)
	if err != nil || len(codes) != 8 {
		t.Fatal("factor activation", err)
	}
	if _, err = f.app.Auth.Resolve(f.ctx, other, false); !errors.Is(err, auth.ErrSession) {
		t.Fatal("factor activation left other session live", err)
	}
	if err = f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, ""); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("factorless confirmation accepted", err)
	}
	if err = f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, codes[0]); err != nil {
		t.Fatal("recovery confirmation", err)
	}
	if err = f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, codes[0]); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("reused recovery accepted", err)
	}
	replacement, err := f.app.Auth.RegenerateRecoveryCodes(f.ctx, f.identity)
	if err != nil || len(replacement) != 8 {
		t.Fatal("regenerate", err)
	}
	if err = f.app.Auth.Reauthenticate(f.ctx, f.identity, integrationPassword, codes[1]); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("old recovery still valid", err)
	}
	status, err := f.app.Auth.SecurityStatus(f.ctx, f.identity)
	if err != nil || !status.TOTPEnabled || status.RecoveryRemaining != 8 {
		t.Fatal("factor status", status, err)
	}
	if err = f.app.Auth.DisableTOTP(f.ctx, f.identity); err != nil {
		t.Fatal("factor disable", err)
	}
	status, err = f.app.Auth.SecurityStatus(f.ctx, f.identity)
	if err != nil || status.TOTPEnabled || status.RecoveryRemaining != 0 {
		t.Fatal("factor material retained", status, err)
	}
	other, _, err = f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.2", "Other device")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Auth.RevokeOtherSessions(f.ctx, f.identity); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Auth.Resolve(f.ctx, other, false); !errors.Is(err, auth.ErrSession) {
		t.Fatal("other session survives", err)
	}
	if _, err = f.app.Auth.Resolve(f.ctx, f.token, false); err != nil {
		t.Fatal("current session lost", err)
	}
}
