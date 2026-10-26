//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestPantrySuggestionsIndependentEntryClaimsAndCurrentReader(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	catalog, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Pantry suggestion mapping"})
	ingredientBind(t, f, catalog, product)
	rid := suggestionRecipe(t, f, catalog, "Independent Pantry alternative", "0.8")
	if _, err := reserveFixtureLot(t, f, pid, mid, product, lot, "0.4"); err != nil {
		t.Fatal(err)
	}
	reader := shoppingTestMember(t, f, "pantry-suggestions-reader", "reader")
	_, actualReader, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "Pantry reader")
	if err != nil {
		t.Fatal(err)
	}
	query := store.RecipeSuggestionQuery{EntryModule: "pantry", Servings: "1"}
	page, err := f.app.Store.RecipeSuggestions(f.ctx, actualReader, query)
	if err != nil || suggestionCandidate(t, page, rid).Outcome != "shortage" || page.EntryModule != "pantry" || !page.MealplanEnabled {
		t.Fatal("Pantry entry ignored active claims or actual reader", page, err)
	}
	row := suggestionCandidate(t, page, rid)
	if len(row.Products) != 1 || row.Products[0].Free.Rational != "3/5" || row.Products[0].Shortage.Rational != "1/5" {
		t.Fatal("free amount was not exact", row)
	}
	state := f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	page, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, query)
	if err != nil || page.MealplanEnabled || suggestionCandidate(t, page, rid).Outcome != "sufficient" || page.Generation != state.Generation {
		t.Fatal("Pantry depended on Mealplan or ignored atomic release", page, err)
	}
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, store.RecipeSuggestionQuery{Servings: "1"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("default entry bypassed disabled Mealplan", err)
	}
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, store.RecipeSuggestionQuery{EntryModule: "expenses", Servings: "1"}); err == nil {
		t.Fatal("arbitrary entry authority accepted")
	}
	state = f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	page, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, query)
	if err != nil || page.RecipesEnabled || len(page.Candidates) != 0 {
		t.Fatal("disabled Recipes disclosed source data", page, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", actualReader.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, query); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked session disclosed even empty state", err)
	}
}

func TestPantrySuggestionsHTTPEntryCursorScopeAndPagePrivacy(t *testing.T) {
	f, _, _, _, _ := reservationFixture(t)
	for i := range 21 {
		auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Pantry cursor " + strings.Repeat("Z", 21-i), "base_servings": "1"})
	}
	get := func(path, token string, status int) suggestionResponse {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, path, token, "", "", nil, nil)
		if err != nil || response.StatusCode != status {
			t.Fatal("entry HTTP result", status, string(raw), err)
		}
		var page suggestionResponse
		if strings.HasPrefix(path, "/api/") && status == 200 {
			if response.Header.Get("Cache-Control") != "private, no-store" || json.Unmarshal(raw, &page) != nil {
				t.Fatal("private typed response missing", response.Header, string(raw))
			}
		}
		return page
	}
	const path = "/api/v1/pantry/recipe-suggestions"
	pantry := get(path, f.token, 200)
	mealplan := get("/api/v1/planning/suggestions", f.token, 200)
	if pantry.Next == "" || len(pantry.Candidates) != 20 || pantry.EntryModule != "pantry" {
		t.Fatal("missing bounded Pantry continuation", pantry)
	}
	get(path+"?cursor="+url.QueryEscape(mealplan.Next), f.token, 422)
	get("/api/v1/planning/suggestions?cursor="+url.QueryEscape(pantry.Next), f.token, 422)
	next := get(path+"?cursor="+url.QueryEscape(pantry.Next), f.token, 200)
	if len(next.Candidates) != 1 || next.Next != "" {
		t.Fatal("own Pantry continuation failed", next)
	}
	get(path+"?entry_module=mealplan", f.token, 422)
	get(path, "", 401)
	get("/m/pantry/recipe-suggestions", f.token, 200)
	state := f.app.Store.States()["mealplan"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	get(path+"?cursor="+url.QueryEscape(pantry.Next), f.token, 409)
	get(path, f.token, 200)
	get("/m/pantry/recipe-suggestions", f.token, 200)
	get("/api/v1/planning/suggestions", f.token, 404)
	state = f.app.Store.States()["pantry"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	get(path, f.token, 404)
	get("/m/pantry/recipe-suggestions", f.token, 404)
}
