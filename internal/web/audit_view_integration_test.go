//go:build integration

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"household/internal/value"
)

func TestAuditViewerBoundaries(t *testing.T) {
	f := newIntegrationFixture(t)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO shopping.lists(household_id,name,created_by)
		SELECT $1,'Audit row '||n,$2 FROM generate_series(1,55) n`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	sentinel, resource := "<img src=x onerror=alert(1)>", value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO shopping.lists(id,household_id,name,description,created_by) VALUES($1,$2,$3,'private-note-sentinel',$4)", resource, f.identity.HouseholdID, sentinel, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	other := f.identity
	other.HouseholdID = value.UUID()
	request := httptest.NewRequest(http.MethodGet, "/settings/audit", nil).WithContext(context.WithValue(f.ctx, identityKey, other))
	foreign, err := f.app.readAudit(request)
	if err != nil || len(foreign.Entries) != 0 {
		t.Fatalf("audit household scoping: %d/%v", len(foreign.Entries), err)
	}
	read := func(path, token string) (*integrationResponse, []byte) {
		t.Helper()
		response, data, err := f.request(http.MethodGet, path, token, "", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return response, data
	}
	response, data := read("/api/v1/core/audit?module=shopping&action=CREATE", f.token)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status/cache: %d/%s", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	if strings.Contains(string(data), "private-note-sentinel") || strings.Contains(string(data), "foreign-audit-sentinel") {
		t.Fatal("private or foreign audit content exposed")
	}
	legacyRequest := value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.audit_log(actor_user_id,action,outcome,request_id) VALUES($1,'operator_password_recovery','success',$2)", f.identity.UserID, legacyRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.audit_log(action,outcome,request_id) VALUES('unscoped-private','success',$1)", value.UUID()); err != nil {
		t.Fatal(err)
	}
	legacyResponse, legacyData := read("/api/v1/core/audit?module=core&resource="+f.identity.UserID+"&request="+legacyRequest, f.token)
	var legacy auditResult
	if err := json.Unmarshal(legacyData, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacyResponse.StatusCode != 200 || len(legacy.Entries) != 1 || legacy.Entries[0].ActorID != "" || legacy.Entries[0].Resource != f.identity.UserID || len(legacy.Entries[0].Changes) != 0 {
		t.Fatalf("legacy recovery projection: %s", legacyData)
	}
	_, allData := read("/api/v1/core/audit", f.token)
	if strings.Contains(string(allData), "unscoped-private") {
		t.Fatal("unscoped legacy row exposed")
	}
	var first auditResult
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 50 || first.Next == 0 {
		t.Fatalf("bounded first page: %d/%d", len(first.Entries), first.Next)
	}
	_, data = read("/api/v1/core/audit?module=shopping&action=CREATE&before="+strconv.FormatInt(first.Next, 10), f.token)
	var second auditResult
	if err := json.Unmarshal(data, &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 6 || second.Next != 0 {
		t.Fatalf("second page: %d/%d", len(second.Entries), second.Next)
	}
	for _, entry := range second.Entries {
		if entry.ID >= first.Next {
			t.Fatal("cursor page overlapped")
		}
	}
	response, data = read("/settings/audit?resource="+resource, f.token)
	if response.StatusCode != 200 || strings.Contains(string(data), sentinel) || !strings.Contains(string(data), "&lt;img") {
		t.Fatalf("HTML escaping/render: %d", response.StatusCode)
	}
	for _, query := range []string{"before=-1", "resource=no", "request=no", "module=other"} {
		response, _ = read("/api/v1/core/audit?"+query, f.token)
		if response.StatusCode != 422 {
			t.Fatalf("invalid filter %s: %d", query, response.StatusCode)
		}
	}
	response, _ = read("/api/v1/core/audit", "")
	if response.StatusCode != 401 {
		t.Fatalf("anonymous audit: %d", response.StatusCode)
	}
	for _, role := range []string{"reader", "editor"} {
		if _, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET role=$1 WHERE user_id=$2", role, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/settings/audit", "/api/v1/core/audit"} {
			response, _ = read(path, f.token)
			if response.StatusCode != 403 {
				t.Fatalf("%s at %s: %d", role, path, response.StatusCode)
			}
		}
	}
}

func TestAuditRequestIdentity(t *testing.T) {
	f := newIntegrationFixture(t)
	csrf := f.app.Auth.CSRF(f.token, "session")
	spoof := value.UUID()
	body := `{"data":{"name":"Request provenance"},"generation":1}`
	response, _, err := f.request(http.MethodPost, "/api/v1/m/shopping/lists", f.token, csrf, "application/json", strings.NewReader(body), map[string]string{"X-Request-ID": spoof, "Idempotency-Key": value.UUID()})
	if err != nil {
		t.Fatal(err)
	}
	request := response.Header.Get("X-Request-ID")
	if response.StatusCode != 200 || request == spoof || !value.ValidUUID(request) {
		t.Fatalf("server request identity: status=%d request=%q", response.StatusCode, request)
	}
	var actor string
	if err = f.owner.QueryRow(f.ctx, "SELECT actor_user_id FROM core.audit_log WHERE request_id=$1 AND action='CREATE'", request).Scan(&actor); err != nil || actor != f.identity.UserID {
		t.Fatalf("actor correlation: %s/%v", actor, err)
	}
	if _, _, err = f.app.Auth.Login(f.ctx, "bootstrap", "invalid password", "", "127.0.0.2", "not logged"); err == nil {
		t.Fatal("invalid login accepted")
	}
	var failed int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE action='login_failed' AND actor_user_id IS NULL AND outcome='failure'").Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("rejected login audit: %d/%v", failed, err)
	}
}
