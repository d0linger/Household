//go:build integration

package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/store"
)

func financialSurfaceEntries(t *testing.T, f *integrationFixture, prefix string, count int, created string) []string {
	t.Helper()
	rows, err := f.owner.Query(f.ctx, `INSERT INTO expenses.entries(household_id,occurred_on,description,amount_minor,created_by,created_at)
 SELECT $1,'2026-10-26',$2||' '||n,100,$3,$4::timestamptz+n*interval '1 second' FROM generate_series(1,$5::int)n RETURNING id`,
		f.identity.HouseholdID, prefix, f.identity.UserID, created, count)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil || len(ids) != count {
		t.Fatal("financial surface fixture rows", len(ids), err)
	}
	return ids
}

func financialSurfaceRetire(t *testing.T, f *integrationFixture, ids []string) {
	t.Helper()
	// Owner corruption is intentional: runtime guards reject retirement of a
	// live source. Readers must nevertheless deny a damaged restored identity.
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=ANY($1::uuid[])", ids); err != nil {
		t.Fatal(err)
	}
}

func TestFinancialSurfaceSearchFiltersBeforeRankLimitAndSurvivesDisabledExpenses(t *testing.T) {
	f := newIntegrationFixture(t)
	visible := financialSurfaceEntries(t, f, "Surface visible", 3, "2026-01-01")
	hidden := financialSurfaceEntries(t, f, "Surface hidden", 55, "2026-02-01")
	financialSurfaceRetire(t, f, hidden)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body)
 SELECT t.household_id,'expenses',m.data_generation,'entries',t.id,t.description,'' FROM expenses.entries t
 JOIN core.module_states m ON m.household_id=t.household_id AND m.module_id='expenses' WHERE t.household_id=$1`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	shopping, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Surface shopping"})
	rows, err := f.app.Store.Search(f.ctx, f.identity, "Surface")
	if err != nil || len(rows) != 4 {
		t.Fatal("hidden-first ranked rows displaced visible results", rows, err)
	}
	want := map[string]bool{store.String(shopping["id"]): true}
	for _, id := range visible {
		want[id] = true
	}
	for _, row := range rows {
		if !want[store.String(row["id"])] {
			t.Fatal("unbound financial search result", row)
		}
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='expenses'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	rows, err = f.app.Store.Search(f.ctx, f.identity, "Surface")
	if err != nil || len(rows) != 1 || store.String(rows[0]["id"]) != store.String(shopping["id"]) {
		t.Fatal("disabled Expenses prevented unrelated current search", rows, err)
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/core/search?q=Surface", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || strings.Contains(string(raw), "Surface visible") || !strings.Contains(string(raw), "Surface shopping") {
		t.Fatal("HTTP search bypassed current module snapshot", response, string(raw), err)
	}
}

func TestFinancialSurfaceReferencePagingAndSelectedHydrationFilterBeforeLimits(t *testing.T) {
	f := newIntegrationFixture(t)
	visible := financialSurfaceEntries(t, f, "Reference visible", 105, "2026-01-01")
	hidden := financialSurfaceEntries(t, f, "Reference hidden", 55, "2026-02-01")
	financialSurfaceRetire(t, f, hidden)
	const path = "/api/v1/core/reference-options/expenses/entries"
	get := func(suffix string) referencePage {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, path+suffix, f.token, "", "", nil, nil)
		var page referencePage
		if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &page) != nil || strings.Contains(string(raw), "Reference hidden") || strings.Contains(string(raw), "amount_minor") {
			t.Fatal("financial reference leaked/failed", response, string(raw), err)
		}
		return page
	}
	first := get("")
	second := get("?cursor=" + url.QueryEscape(first.Next))
	third := get("?cursor=" + url.QueryEscape(second.Next))
	if len(first.Records) != 50 || first.Next == "" || len(second.Records) != 50 || second.Next == "" || len(third.Records) != 5 || third.Next != "" {
		t.Fatal("pre-limit visibility or cursor lookahead lost visible references", first, second, third)
	}
	seen := map[string]bool{}
	for _, page := range []referencePage{first, second, third} {
		for _, option := range page.Records {
			if seen[option.ID] {
				t.Fatal("reference duplicated across a cursor boundary", option)
			}
			seen[option.ID] = true
		}
	}
	if len(seen) != len(visible) || !seen[visible[0]] {
		t.Fatal("reference pagination omitted visible values", len(seen))
	}
	if page := get("/" + hidden[0]); len(page.Records) != 0 || page.Next != "" {
		t.Fatal("direct selected hydration exposed hidden financial identity", page)
	}
	if page := get("/" + visible[0]); len(page.Records) != 1 || page.Records[0].ID != visible[0] {
		t.Fatal("visible selected hydration outside the first page failed", page)
	}
	rows, err := f.app.Store.FinancialReferences(f.ctx, f.identity, "entries", visible[0])
	if err != nil || len(rows) != 101 || store.String(rows[100]["id"]) != visible[0] {
		t.Fatal("selected form parent beyond first hundred lost its authorized label", len(rows), err)
	}
	rows, err = f.app.Store.FinancialReferences(f.ctx, f.identity, "entries", hidden[0])
	if err != nil || len(rows) != 100 {
		t.Fatal("hidden form parent was appended outside the visible first page", len(rows), err)
	}
	for _, row := range rows {
		if strings.Contains(store.String(row["description"]), "hidden") {
			t.Fatal("hidden full record entered form references", row)
		}
	}
	response, _, err := f.request(http.MethodGet, path+"?q=changed&cursor="+url.QueryEscape(first.Next), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 422 {
		t.Fatal("changed query accepted another reference scope", response, err)
	}
}

func TestFinancialSurfaceRelatedReportsAndBothCSVEntitiesExcludeBrokenBindings(t *testing.T) {
	f, category, entry, line := expenseIntegrityFixture(t)
	hidden, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Excluded secret expense", "amount_minor": "20.00"})
	hiddenLine, _ := auditCreate(t, f, "expenses", "entry_lines", store.Record{"entry_id": hidden["id"], "category_id": category["id"], "amount_minor": "20.00", "note": "Excluded secret line"})
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
 SELECT $1,$2,$3,1,'Excluded secret first rows',-1000+n,$4 FROM generate_series(1,105)n`,
		f.identity.HouseholdID, hidden["id"], category["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entry_lines SET amount_minor=1895 WHERE id=$1", hiddenLine["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp() WHERE household_id=$1", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	financialSurfaceRetire(t, f, []string{store.String(hidden["id"])})
	months, err := f.app.Store.ExpenseReport(f.ctx, f.identity)
	if err != nil || len(months) != 1 || store.String(months[0]["amount_minor"]) != "1000" {
		t.Fatal("monthly SUM included a hidden financial source", months, err)
	}
	related, err := f.app.Store.FinancialRelated(f.ctx, f.identity, "categories", store.String(category["id"]))
	if err != nil || len(related) != 1 || store.String(related[0]["id"]) != store.String(line["id"]) {
		t.Fatal("shared category exposed hidden financial children", related, err)
	}
	for _, entity := range []string{"entries", "entry_lines"} {
		response, raw, err := f.request(http.MethodGet, "/api/v1/m/expenses/"+entity+"/export.csv", f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 200 || strings.Contains(string(raw), "Excluded secret") || strings.Contains(string(raw), store.String(hiddenLine["id"])) {
			t.Fatal("financial CSV exposed hidden parent/child facts", entity, response, string(raw), err)
		}
		rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
		if err != nil || len(rows) != 2 {
			t.Fatal("financial CSV omitted or duplicated visible facts", entity, rows, err)
		}
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.financial_subjects WHERE resource_type='entry_lines' AND resource_id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	months, err = f.app.Store.ExpenseReport(f.ctx, f.identity)
	if err != nil || len(months) != 0 {
		t.Fatal("incomplete child binding allowed the parent SUM", months, err)
	}
	related, err = f.app.Store.FinancialRelated(f.ctx, f.identity, "entries", store.String(entry["id"]))
	if err != nil || len(related) != 0 {
		t.Fatal("related rows returned an incomplete financial set", related, err)
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/core/reference-options/expenses/categories/"+store.String(category["id"]), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), store.String(category["name"])) {
		t.Fatal("shared category master data incorrectly inherited hidden entry access", response, string(raw), err)
	}
}

func TestFinancialSurfaceCSVUsesOneAuthorizedSnapshotAcrossPages(t *testing.T) {
	f := newIntegrationFixture(t)
	ids := financialSurfaceEntries(t, f, "Snapshot original", 150, "2026-01-01")
	target := ids[0]
	var all []store.Record
	pages := 0
	err := f.app.Store.ExportFinancial(f.ctx, f.identity, "entries", func(page []store.Record) error {
		pages++
		all = append(all, page...)
		if pages == 1 {
			if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entries SET description='AFTER SNAPSHOT' WHERE id=$1", target); err != nil {
				return err
			}
			financialSurfaceRetire(t, f, []string{target})
			financialSurfaceEntries(t, f, "AFTER SNAPSHOT inserted", 1, "2026-03-01")
			if _, err := f.owner.Exec(f.ctx, "UPDATE core.users SET auth_version=auth_version+1 WHERE id=$1", f.identity.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || pages != 2 || len(all) != 150 {
		t.Fatal("financial CSV changed its authorized content snapshot between pages", pages, len(all), err)
	}
	seen := map[string]bool{}
	for _, row := range all {
		id := store.String(row["id"])
		if seen[id] || strings.Contains(store.String(row["description"]), "AFTER SNAPSHOT") {
			t.Fatal("CSV duplicated rows or mixed later financial facts", row)
		}
		seen[id] = true
	}
	if !seen[target] {
		t.Fatal("later retirement partially removed a previously authorized snapshot")
	}
	emitted := false
	if err = f.app.Store.ExportFinancial(f.ctx, f.identity, "entries", func([]store.Record) error { emitted = true; return nil }); !errors.Is(err, store.ErrForbidden) || emitted {
		t.Fatal("new export accepted the superseded authenticated snapshot", emitted, err)
	}
}

func TestFinancialSurfaceCurrentAuthDeniesEveryIndirectService(t *testing.T) {
	for _, test := range []struct{ name, statement, target string }{
		{"revoked session", "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", "session"},
		{"expired session", "UPDATE core.sessions SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "session"},
		{"inactive membership", "UPDATE core.memberships SET inactive_at=clock_timestamp() WHERE id=$1", "membership"},
		{"changed role", "UPDATE core.memberships SET role='reader' WHERE id=$1", "membership"},
		{"changed auth version", "UPDATE core.users SET auth_version=auth_version+1 WHERE id=$1", "user"},
		{"forced password change", "UPDATE core.users SET must_change_password=true WHERE id=$1", "user"},
		{"disabled user", "UPDATE core.users SET disabled_at=clock_timestamp() WHERE id=$1", "user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _, entry, _ := expenseIntegrityFixture(t)
			target := f.identity.UserID
			switch test.target {
			case "session":
				target = f.identity.SessionID
			case "membership":
				target = f.identity.MembershipID
			}
			if _, err := f.owner.Exec(f.ctx, test.statement, target); err != nil {
				t.Fatal(err)
			}
			if rows, err := f.app.Store.Search(f.ctx, f.identity, ""); !errors.Is(err, store.ErrForbidden) || rows != nil {
				t.Fatal("empty search bypassed current authentication", rows, err)
			}
			if rows, err := f.app.Store.FinancialReferenceOptions(f.ctx, f.identity, "entries", store.FinancialReferenceQuery{Generation: 1}); !errors.Is(err, store.ErrForbidden) || rows != nil {
				t.Fatal("options bypassed current authentication", rows, err)
			}
			if rows, err := f.app.Store.FinancialReferences(f.ctx, f.identity, "entries", store.String(entry["id"])); !errors.Is(err, store.ErrForbidden) || rows != nil {
				t.Fatal("selected hydration bypassed current authentication", rows, err)
			}
			if rows, err := f.app.Store.FinancialRelated(f.ctx, f.identity, "entries", store.String(entry["id"])); !errors.Is(err, store.ErrForbidden) || rows != nil {
				t.Fatal("related rows bypassed current authentication", rows, err)
			}
			if rows, err := f.app.Store.ExpenseReport(f.ctx, f.identity); !errors.Is(err, store.ErrForbidden) || rows != nil {
				t.Fatal("report bypassed current authentication", rows, err)
			}
			emitted := false
			if err := f.app.Store.ExportFinancial(f.ctx, f.identity, "entries", func([]store.Record) error { emitted = true; return nil }); !errors.Is(err, store.ErrForbidden) || emitted {
				t.Fatal("CSV bypassed current authentication", emitted, err)
			}
		})
	}
}

func TestFinancialSurfaceCSVCancellationAndOutputBoundsNeverPublishPartialContent(t *testing.T) {
	f := newIntegrationFixture(t)
	ids := financialSurfaceEntries(t, f, "Bounded export", 101, "2026-01-01")
	ctx, cancel := context.WithCancel(f.ctx)
	pages := 0
	err := f.app.Store.ExportFinancial(ctx, f.identity, "entries", func([]store.Record) error { pages++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || pages != 1 {
		t.Fatal("cancelled export continued paging or succeeded", pages, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE expenses.entries SET payee=repeat('oversized financial payload ',700000) WHERE id=$1", ids[0]); err != nil {
		t.Fatal(err)
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/m/expenses/entries/export.csv", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 422 || strings.Contains(string(raw), "Bounded export") || strings.Contains(string(raw), "oversized financial payload") || strings.Contains(response.Header.Get("Content-Type"), "text/csv") {
		t.Fatal("oversized export published buffered financial pages", response, len(raw), err)
	}
	if err = f.pool.Ping(f.ctx); err != nil {
		t.Fatal("cancelled or bounded export leaked its database connection", err)
	}
}

func TestFinancialSurfaceOptionsRejectStaleGenerationAndDisabledModule(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	st := f.app.Store.States()["expenses"]
	if rows, err := f.app.Store.FinancialReferenceOptions(f.ctx, f.identity, "entries", store.FinancialReferenceQuery{SelectedID: store.String(entry["id"]), Generation: st.Generation + 1}); !errors.Is(err, store.ErrConflict) || rows != nil {
		t.Fatal("cursor scope accepted another data generation", rows, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='expenses'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if rows, err := f.app.Store.FinancialReferenceOptions(f.ctx, f.identity, "entries", store.FinancialReferenceQuery{Generation: st.Generation}); !errors.Is(err, store.ErrNotFound) || rows != nil {
		t.Fatal("cached enabled state authorized options from a disabled module", rows, err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	if err := f.pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
