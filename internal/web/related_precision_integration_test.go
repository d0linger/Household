//go:build integration

package web

import (
	"net/http"
	"strings"
	"testing"

	"household/internal/value"
)

func TestRelatedAmountAndSelectedReference(t *testing.T) {
	f := newIntegrationFixture(t)
	entry, category := value.UUID(), value.UUID()
	for _, query := range []string{
		"INSERT INTO expenses.categories(id,household_id,name,created_by) VALUES($1,$2,'Precision',$3)",
	} {
		if _, err := f.owner.Exec(f.ctx, query, category, f.identity.HouseholdID, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO expenses.entries(id,household_id,occurred_on,description,amount_minor,created_by) VALUES($1,$2,current_date,'Exact related amount',1000000,$3)", entry, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,created_by) VALUES($1,$2,$3,1000000,$4)", f.identity.HouseholdID, entry, category, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	response, data, err := f.request(http.MethodGet, "/m/expenses/entries/"+entry, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), "<small>10000,00 €</small>") {
		t.Fatalf("related amount lost precision: %v/%d", err, response.StatusCode)
	}
	parent, item := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO shopping.lists(id,household_id,name,sort_order,created_by) VALUES($1,$2,'Selected beyond page',1000,$3)", parent, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO shopping.lists(household_id,name,created_by) SELECT $1,'Other '||n,$2 FROM generate_series(1,105)n", f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO shopping.items(id,household_id,list_id,name,created_by) VALUES($1,$2,$3,'Keep parent',$4)", item, f.identity.HouseholdID, parent, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	response, data, err = f.request(http.MethodGet, "/m/shopping/items/"+item, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), `value="`+parent+`" selected`) {
		t.Fatalf("selected reference missing beyond first100: %v/%d", err, response.StatusCode)
	}
}
