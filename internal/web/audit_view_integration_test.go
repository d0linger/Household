//go:build integration

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	malformedRequest := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.audit_log(actor_user_id,action,outcome,request_id,changes)
 VALUES($1,'operator_password_recovery','success',$2,'{"description":{"after":"private-legacy-finance-sentinel"}}'::jsonb)`,
		f.identity.UserID, malformedRequest); err != nil {
		t.Fatal(err)
	}
	malformedResponse, malformedData := read("/api/v1/core/audit?module=core&resource="+f.identity.UserID+"&request="+malformedRequest, f.token)
	var malformed auditResult
	if json.Unmarshal(malformedData, &malformed) != nil || malformedResponse.StatusCode != 200 || len(malformed.Entries) != 0 {
		t.Fatal("data-bearing unscoped row bypassed the exact legacy shape")
	}
	_, allData := read("/api/v1/core/audit", f.token)
	if strings.Contains(string(allData), "unscoped-private") || strings.Contains(string(allData), "private-legacy-finance-sentinel") {
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

func TestAuditViewerActorNamesAndSearch(t *testing.T) {
	f := newIntegrationFixture(t)
	former, foreign, missing := value.UUID(), value.UUID(), value.UUID()
	for _, account := range []struct{ id, name string }{{former, "former.audit"}, {foreign, "foreign.audit"}} {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.users(id,username,display_name,password_hash,disabled_at)
 VALUES($1,$2,'Audit name fixture','not-a-login-hash',now())`, account.id, account.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.memberships(household_id,user_id,role,inactive_at) VALUES($1,$2,'reader',now())`, f.identity.HouseholdID, former); err != nil {
		t.Fatal(err)
	}
	insert := func(actor, kind, label string) string {
		t.Helper()
		request := value.UUID()
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.audit_log(household_id,actor_user_id,actor_kind,module_id,resource_type,action,outcome,request_id,entity_label)
 VALUES($1,nullif($2,'')::uuid,$3,'shopping','lists','UPDATE','success',$4,$5)`, f.identity.HouseholdID, actor, kind, request, label); err != nil {
			t.Fatal(err)
		}
		return request
	}
	currentRequest := insert(f.identity.UserID, "user", "Audit literal 100%_done")
	formerRequest := insert(former, "user", "Former audit member")
	foreignRequest := insert(foreign, "user", "Foreign unresolved actor")
	missingRequest := insert(missing, "user", "Removed audit actor")
	workerRequest := insert("", "worker", "Background work")
	read := func(query string) auditResult {
		t.Helper()
		response, data, err := f.request(http.MethodGet, "/api/v1/core/audit?"+query, f.token, "", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var result auditResult
		if response.StatusCode != 200 || json.Unmarshal(data, &result) != nil {
			t.Fatalf("audit response: %d %s", response.StatusCode, data)
		}
		return result
	}
	for _, tc := range []struct{ name, request, username string }{
		{"current user", currentRequest, f.identity.Username},
		{"disabled inactive member", formerRequest, "former.audit"},
		{"foreign user has no name", foreignRequest, ""},
		{"removed user has no name", missingRequest, ""},
		{"worker has no user name", workerRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := read("request=" + tc.request)
			if len(result.Entries) != 1 || result.Entries[0].ActorUsername != tc.username {
				t.Fatalf("actor result: %+v", result.Entries)
			}
		})
	}
	for _, query := range []string{"%_", "AUDIT LITERAL 100%_DONE"} {
		result := read("q=" + url.QueryEscape(query))
		if len(result.Entries) != 1 || result.Entries[0].Request != currentRequest {
			t.Fatalf("literal bounded search %q: %+v", query, result.Entries)
		}
	}
	if result := read("q=former.audit"); len(result.Entries) != 1 || result.Entries[0].Request != formerRequest {
		t.Fatal("current username search missed inactive member")
	}
	if result := read("q=foreign.audit"); len(result.Entries) != 0 {
		t.Fatal("search exposed an account outside this household")
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.users SET username='renamed.audit' WHERE id=$1", former); err != nil {
		t.Fatal(err)
	}
	if result := read("request=" + formerRequest); len(result.Entries) != 1 || result.Entries[0].ActorUsername != "renamed.audit" {
		t.Fatal("username lookup falsely presents an old snapshot")
	}
	var raw string
	if err := f.owner.QueryRow(f.ctx, "SELECT changes::text||details::text FROM core.audit_log WHERE request_id=$1", formerRequest).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "former.audit") || strings.Contains(raw, "renamed.audit") {
		t.Fatal("name presentation wrote identity text into audit history")
	}
	for range 52 {
		insert(f.identity.UserID, "user", "Audit searchable pagination")
	}
	request := httptest.NewRequest(http.MethodGet, "/settings/audit?q=Audit+searchable+pagination&action=UPDATE", nil).WithContext(context.WithValue(f.ctx, identityKey, f.identity))
	first, err := f.app.readAudit(request)
	if err != nil || len(first.Entries) != 50 || first.NextURL == "" {
		t.Fatalf("search pagination: %d/%v", len(first.Entries), err)
	}
	next, err := url.Parse(first.NextURL)
	if err != nil || next.Query().Get("q") != "Audit searchable pagination" || next.Query().Get("action") != "UPDATE" {
		t.Fatalf("search filters lost: %s", first.NextURL)
	}
	request = httptest.NewRequest(http.MethodGet, first.NextURL, nil).WithContext(context.WithValue(f.ctx, identityKey, f.identity))
	second, err := f.app.readAudit(request)
	if err != nil || len(second.Entries) != 2 || second.NextURL != "" || second.FirstURL == "" {
		t.Fatalf("final search page: %d/%v", len(second.Entries), err)
	}
	newest, err := url.Parse(second.FirstURL)
	if err != nil || newest.Query().Get("before") != "" || newest.Query().Get("q") != "Audit searchable pagination" {
		t.Fatalf("newest link lost filters: %s", second.FirstURL)
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
