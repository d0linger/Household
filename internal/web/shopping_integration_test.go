//go:build integration

package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"household/internal/value"
)

func TestShoppingBoardPaginationAndScope(t *testing.T) {
	f := newIntegrationFixture(t)
	list, other := value.UUID(), value.UUID()
	for _, id := range []string{list, other} {
		if _, err := f.owner.Exec(f.ctx, "INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,'Board test',$3)", id, f.identity.HouseholdID, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO shopping.items(household_id,list_id,name,group_name,created_by,created_at,checked_at,checked_by) SELECT $1,$2,'Artikel '||n,'Gemüse',$3,'2026-01-01'::timestamptz,CASE WHEN n<=5 THEN now() END,CASE WHEN n<=5 THEN $3::uuid END FROM generate_series(1,105)n", f.identity.HouseholdID, list, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO shopping.items(household_id,list_id,name,created_by) VALUES($1,$2,'Other list private',$3)", f.identity.HouseholdID, other, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	type boardPage struct {
		Records                      []map[string]any `json:"records"`
		Next                         string           `json:"next_cursor"`
		Total, Completed, Generation int64
	}
	get := func(path string, status int) boardPage {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("status %d want %d: %s", response.StatusCode, status, raw)
		}
		if strings.Contains(string(raw), "Other list private") {
			t.Fatal("cross-list leakage")
		}
		var p boardPage
		if status == 200 && json.Unmarshal(raw, &p) != nil {
			t.Fatal("invalid board response")
		}
		return p
	}
	path := "/api/v1/shopping/lists/" + list + "/items"
	first := get(path, 200)
	if len(first.Records) != 100 || first.Total != 105 || first.Completed != 5 || first.Next == "" || first.Generation != 1 {
		t.Fatalf("incorrect totals/page: %+v", first)
	}
	second := get(path+"?cursor="+url.QueryEscape(first.Next), 200)
	if len(second.Records) != 5 || second.Next != "" {
		t.Fatal("incorrect tail page")
	}
	seen := map[string]bool{}
	for _, p := range []boardPage{first, second} {
		for _, row := range p.Records {
			id := row["id"].(string)
			if seen[id] {
				t.Fatal("duplicate page row")
			}
			seen[id] = true
		}
	}
	filtered := get(path+"?q=Artikel%20105", 200)
	if len(filtered.Records) != 1 || filtered.Total != 105 || filtered.Completed != 5 {
		t.Fatal("search not global or totals filtered")
	}
	get(path+"?q=other&cursor="+url.QueryEscape(first.Next), 422)
	get("/api/v1/shopping/lists/"+other+"/items?cursor="+url.QueryEscape(first.Next), 422)
	get("/api/v1/shopping/lists/"+value.UUID()+"/items", 404)
	// Names may legitimately contain characters that JSON HTML-escapes. The
	// signed cursor produced for the final page row must remain readable.
	if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.items SET name=$3,group_name=$4 WHERE household_id=$1 AND list_id=$2", f.identity.HouseholdID, list, strings.Repeat("<", 200), strings.Repeat("<", 300)); err != nil {
		t.Fatal(err)
	}
	longNames := get(path, 200)
	if len(longNames.Next) <= 4096 || len(get(path+"?cursor="+url.QueryEscape(longNames.Next), 200).Records) != 5 {
		t.Fatal("valid escaped names produced an unreadable cursor")
	}
	response, raw, err := f.request(http.MethodGet, "/m/shopping/lists/"+list, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), `id="shopping-quick-add"`) {
		t.Fatal("board not available on list detail")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=now() WHERE id=$1", list); err != nil {
		t.Fatal(err)
	}
	response, raw, err = f.request(http.MethodGet, "/m/shopping/lists/"+list, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || strings.Contains(string(raw), `id="shopping-quick-add"`) {
		t.Fatal("archived list offers new items")
	}
}
