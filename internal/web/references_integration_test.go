//go:build integration

package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/value"
)

func TestReferenceOptions(t *testing.T) {
	f := newIntegrationFixture(t)
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO shopping.lists(household_id,name,description,created_by,created_at) SELECT $1,'Same label','private reference note',$2,'2026-01-01'::timestamptz FROM generate_series(1,107)", f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	get := func(path string, status int) referencePage {
		t.Helper()
		r, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != status {
			t.Fatalf("status %d, want %d: %s", r.StatusCode, status, raw)
		}
		if strings.Contains(string(raw), "private reference note") || strings.Contains(string(raw), "created_by") {
			t.Fatal("reference leaked full record")
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("private reference cache policy")
		}
		var page referencePage
		if status == 200 && json.Unmarshal(raw, &page) != nil {
			t.Fatal("invalid page")
		}
		return page
	}
	const path = "/api/v1/core/reference-options/shopping/lists"
	first := get(path, 200)
	if len(first.Records) != 50 || first.Next == "" {
		t.Fatal("first page/lookahead")
	}
	second := get(path+"?cursor="+url.QueryEscape(first.Next), 200)
	third := get(path+"?cursor="+url.QueryEscape(second.Next), 200)
	seen := map[string]bool{}
	for _, page := range []referencePage{first, second, third} {
		for _, record := range page.Records {
			if seen[record.ID] {
				t.Fatal("duplicate across equal-name/equal-time page boundary")
			}
			seen[record.ID] = true
		}
	}
	if len(seen) != 107 || len(third.Records) != 7 || third.Next != "" {
		t.Fatal("incomplete pagination")
	}
	selected := get(path+"/"+third.Records[0].ID, 200)
	if len(selected.Records) != 1 || selected.Records[0] != third.Records[0] || selected.Next != "" {
		t.Fatal("selected reference beyond first page lost its label")
	}
	if len(get(path+"/"+value.UUID(), 200).Records) != 0 {
		t.Fatal("unknown selected reference returned unrelated records")
	}
	get(path+"/invalid", 404)
	for _, tc := range []struct {
		name, suffix string
		status       int
	}{
		{"changed search", "?q=other&cursor=" + url.QueryEscape(first.Next), 422},
		{"tampered cursor", "?cursor=" + url.QueryEscape(first.Next+"x"), 422},
		{"long search", "?q=" + strings.Repeat("x", 301), 422},
		{"literal wildcard", "?q=%25", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := get(path+tc.suffix, tc.status)
			if tc.status == 200 && len(p.Records) != 0 {
				t.Fatal("wildcard treated as search syntax")
			}
		})
	}
	search := get(path+"?q=Same", 200)
	if len(search.Records) != 50 || search.Next == "" {
		t.Fatal("search paging")
	}
	get("/api/v1/core/reference-options/pantry/products?cursor="+url.QueryEscape(first.Next), 422)
	members := get("/api/v1/core/reference-options/core/memberships", 200)
	if len(members.Records) != 1 || members.Records[0].ID != f.identity.MembershipID {
		t.Fatal("member choices")
	}
	get("/api/v1/core/reference-options/core/users", 404)
	get("/api/v1/core/reference-options/documents/documents", 404)
	readerID := value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.users(id,username,display_name,password_hash,must_change_password) SELECT $1,'reference-reader','Reference reader',password_hash,false FROM core.users WHERE id=$2", readerID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'reader')", f.identity.HouseholdID, readerID); err != nil {
		t.Fatal(err)
	}
	readerToken, _, err := f.app.Auth.Login(f.ctx, "reference-reader", integrationPassword, "", "127.0.0.1", "reference-test")
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodGet, path+"?cursor="+url.QueryEscape(first.Next), readerToken, "", "", nil, nil)
	if err != nil || response.StatusCode != 422 {
		t.Fatal("cursor accepted for a different authenticated user")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.users SET disabled_at=now() WHERE id=$1", readerID); err != nil {
		t.Fatal(err)
	}
	if got := get("/api/v1/core/reference-options/core/memberships", 200); len(got.Records) != 1 {
		t.Fatal("disabled user offered as reference")
	}
	var readerMembership string
	if err = f.owner.QueryRow(f.ctx, "SELECT id FROM core.memberships WHERE user_id=$1", readerID).Scan(&readerMembership); err != nil {
		t.Fatal(err)
	}
	historic := get("/api/v1/core/reference-options/core/memberships/"+readerMembership, 200)
	if len(historic.Records) != 1 || !strings.Contains(historic.Records[0].Label, "nicht mehr aktiv") {
		t.Fatal("historic selected assignment cannot be named")
	}
	response, _, err = f.request(http.MethodGet, path, readerToken, "", "", nil, nil)
	if err != nil || response.StatusCode != 401 {
		t.Fatal("disabled reader retained reference access")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.module_states SET data_generation=data_generation+1 WHERE module_id='shopping'"); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	get(path+"?cursor="+url.QueryEscape(first.Next), 422)
	if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=now() WHERE id=$1", first.Records[0].ID); err != nil {
		t.Fatal(err)
	}
	afterArchive := get(path, 200)
	for _, row := range afterArchive.Records {
		if row.ID == first.Records[0].ID {
			t.Fatal("archived choice")
		}
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE module_id='shopping'"); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	get(path, 404)
}

func TestReferenceCursorBinding(t *testing.T) {
	f := newIntegrationFixture(t)
	c := referenceCursor{Created: time.Now(), ID: value.UUID(), Expires: time.Now().Add(time.Minute).Unix()}
	token, err := f.app.makeReferenceCursor(c, "household/user/query/generation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.readReferenceCursor(token, "household/user/query/generation"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.readReferenceCursor(token, "different-user"); err == nil {
		t.Fatal("cursor accepted outside its scope")
	}
	c.Expires = time.Now().Add(-time.Minute).Unix()
	token, err = f.app.makeReferenceCursor(c, "scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.readReferenceCursor(token, "scope"); err == nil {
		t.Fatal("expired cursor accepted")
	}
}
