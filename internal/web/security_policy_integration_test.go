//go:build integration

package web

import (
	"net/url"
	"strings"
	"testing"

	"household/internal/value"

	"golang.org/x/crypto/bcrypt"
)

// policyAccount creates an active account with a role and signs it in.
func policyAccount(t *testing.T, f *integrationFixture, name, role, userAgent string) (string, string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(integrationPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := value.UUID()
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.users(id,username,display_name,password_hash,must_change_password) VALUES($1,$2,$2,$3,false)`, user, name, string(hash)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,$3)`, f.identity.HouseholdID, user, role); err != nil {
		t.Fatal(err)
	}
	token, _, err := f.app.Auth.Login(f.ctx, name, integrationPassword, "", "127.0.0.1", userAgent)
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}

func policyForm(f *integrationFixture, values url.Values) int {
	response, _, err := f.request("POST", "/settings/security/policy", f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil)
	if err != nil {
		return 0
	}
	return response.StatusCode
}

func TestSecurityPolicyRequiresSecondFactorAfterGrace(t *testing.T) {
	f := newIntegrationFixture(t)
	values := url.Values{"version": {"0"}, "require_admin_mfa": {"on"}, "grace_days": {"0"}}
	if status := policyForm(f, values); status != 422 {
		t.Fatalf("policy without own factor: %d", status)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,last_accepted_counter,enabled_at,encryption_format)
 VALUES($1,'\x00','v1',0,now(),1)`, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if status := policyForm(f, url.Values{"version": {"0"}, "require_editor_mfa": {"on"}, "grace_days": {"0"}}); status != 422 {
		t.Fatalf("editor requirement without administrator requirement: %d", status)
	}
	if status := policyForm(f, values); status != 200 {
		t.Fatalf("save policy: %d", status)
	}
	if status := policyForm(f, values); status != 409 {
		t.Fatalf("stale policy version: %d", status)
	}
	_, admin := policyAccount(t, f, "policy-admin", "admin", "Mozilla/5.0 (Windows NT 10.0) Firefox/140.0")
	_, editor := policyAccount(t, f, "policy-editor", "editor", "Mozilla/5.0 (Windows NT 10.0) Firefox/140.0")
	f.app.invalidatePolicy()
	response, body, err := f.request("GET", "/api/v1/core/today", admin, "", "", nil, nil)
	if err != nil || response.StatusCode != 403 || !strings.Contains(string(body), "mfa_required") {
		t.Fatalf("blocked administrator API: %v %s %v", response, body, err)
	}
	response, _, err = f.request("GET", "/settings", admin, "", "", nil, map[string]string{"Accept": "text/html"})
	if err != nil || response.StatusCode != 303 || response.Header.Get("Location") != "/settings/security" {
		t.Fatalf("blocked administrator page: %v %v", response, err)
	}
	response, body, err = f.request("GET", "/settings/security", admin, "", "", nil, map[string]string{"Accept": "text/html"})
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "zweiten Faktor") {
		t.Fatalf("security settings stay reachable: %v %v", response, err)
	}
	if response, _, err = f.request("GET", "/api/v1/core/today", editor, "", "", nil, nil); err != nil || response.StatusCode != 200 {
		t.Fatalf("editor without requirement: %v %v", response, err)
	}
	if response, _, err = f.request("GET", "/api/v1/core/today", f.token, "", "", nil, nil); err != nil || response.StatusCode != 200 {
		t.Fatalf("administrator with factor: %v %v", response, err)
	}
	// With a grace period the account keeps working and sees a notice.
	if status := policyForm(f, url.Values{"version": {"1"}, "require_admin_mfa": {"on"}, "grace_days": {"14"}}); status != 200 {
		t.Fatalf("grace policy: %d", status)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.security_policies SET enforced_from=now()"); err != nil {
		t.Fatal(err)
	}
	f.app.invalidatePolicy()
	response, body, err = f.request("GET", "/settings", admin, "", "", nil, map[string]string{"Accept": "text/html"})
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Richte ihn bis") {
		t.Fatalf("grace notice: %v %v", response, err)
	}
	var events int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE action='security_policy_changed'").Scan(&events); err != nil || events != 2 {
		t.Fatalf("policy audit events = %d %v", events, err)
	}
}

func TestNewDeviceSignInNotifiesAfterLearningDay(t *testing.T) {
	f := newIntegrationFixture(t)
	const desktop = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/141.0 Safari/537.36"
	const phone = "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Version/19.0 Mobile/15E148 Safari/604.1"
	user, _ := policyAccount(t, f, "device-owner", "editor", desktop)
	notices := func() int {
		var n int
		if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.notifications WHERE user_id=$1 AND kind='security_sign_in_new_device'", user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, _, err := f.app.Auth.Login(f.ctx, "device-owner", integrationPassword, "", "127.0.0.1", phone); err != nil {
		t.Fatal(err)
	}
	if n := notices(); n != 0 {
		t.Fatalf("learning day produced %d notices", n)
	}
	if _, err := f.owner.Exec(f.ctx, "DELETE FROM core.known_devices WHERE user_id=$1 AND first_seen>now()-interval '1 minute' AND device_hash<>(SELECT device_hash FROM core.known_devices WHERE user_id=$1 ORDER BY first_seen LIMIT 1)", user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.known_devices SET first_seen=now()-interval '2 days' WHERE user_id=$1", user); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := f.app.Auth.Login(f.ctx, "device-owner", integrationPassword, "", "127.0.0.1", phone); err != nil {
			t.Fatal(err)
		}
	}
	if n := notices(); n != 1 {
		t.Fatalf("new device notices = %d", n)
	}
	if _, _, err := f.app.Auth.Login(f.ctx, "device-owner", integrationPassword, "", "127.0.0.1", strings.Replace(desktop, "Chrome/141.0", "Chrome/142.0", 1)); err != nil {
		t.Fatal(err)
	}
	if n := notices(); n != 1 {
		t.Fatal("a browser update counted as a new device")
	}
	var devices int
	var raw []byte
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*),min(device_hash) FROM core.known_devices WHERE user_id=$1", user).Scan(&devices, &raw); err != nil || devices != 2 || strings.Contains(string(raw), "Chrome") {
		t.Fatalf("known devices = %d %v", devices, err)
	}
}
