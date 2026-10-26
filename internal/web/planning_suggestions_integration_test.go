//go:build integration

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func suggestionRecipe(t *testing.T, f *integrationFixture, catalog store.Record, title string, quantities ...string) string {
	t.Helper()
	recipe, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": title, "base_servings": "1"})
	rid := store.String(recipe["id"])
	for i, quantity := range quantities {
		data := store.Record{"recipe_id": rid, "name": "Explicit ingredient", "catalog_id": catalog["id"], "unit_code": "kg", "position": i}
		if quantity != "" {
			data["quantity"] = quantity
		}
		auditCreate(t, f, "recipes", "ingredients", data)
	}
	return rid
}

func suggestionCandidate(t *testing.T, page store.RecipeSuggestionPage, rid string) store.RecipeSuggestion {
	t.Helper()
	for _, row := range page.Candidates {
		if row.RecipeID == rid {
			return row
		}
	}
	t.Fatal("expected source missing from the evaluated page", rid, page)
	return store.RecipeSuggestion{}
}

func TestRecipeSuggestionsGroupedClaimsAndIndependentAlternativesReadOnly(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	catalog, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Suggestion explicit identity"})
	ingredientBind(t, f, catalog, product)
	grouped := suggestionRecipe(t, f, catalog, "Suggestion grouped", "0.6", "0.6")
	first := suggestionRecipe(t, f, catalog, "Suggestion independent A", "0.6")
	second := suggestionRecipe(t, f, catalog, "Suggestion independent B", "0.6")
	if _, err := reserveFixtureLot(t, f, pid, mid, product, lot, "0.4"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var out string
		if err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object('audit',(SELECT count(*) FROM core.audit_log),
 'meals',(SELECT count(*) FROM mealplan.meals),'items',(SELECT count(*) FROM shopping.items),
 'claims',(SELECT count(*) FROM core.meal_reservations),'movements',(SELECT count(*) FROM pantry.stock_movements),
 'proofs',(SELECT count(*) FROM core.idempotency_records),'outbox',(SELECT count(*) FROM core.outbox_events))::text`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	page, err := f.app.Store.RecipeSuggestions(f.ctx, f.identity, store.RecipeSuggestionQuery{Search: "Suggestion", Servings: "1"})
	if err != nil || len(page.Candidates) != 3 || page.ReadAt.IsZero() || !page.PantryEnabled || !page.RecipesEnabled || page.More {
		t.Fatal("current independent suggestion page", page, err)
	}
	combined := suggestionCandidate(t, page, grouped)
	if combined.Outcome != "shortage" || len(combined.Products) != 1 || combined.Products[0].Needed.Rational != "6/5" || combined.Products[0].Free.Rational != "3/5" || combined.Products[0].Shortage == nil || combined.Products[0].Shortage.Rational != "3/5" {
		t.Fatal("duplicate ingredients reused stock or active claims were lost", combined)
	}
	for _, rid := range []string{first, second} {
		candidate := suggestionCandidate(t, page, rid)
		if candidate.Outcome != "sufficient" || candidate.Products[0].Free.Rational != "3/5" || candidate.Products[0].Shortage == nil || candidate.Products[0].Shortage.Rational != "0" {
			t.Fatal("independent alternatives consumed one another's stock", candidate)
		}
	}
	if page.Candidates[2].RecipeID != grouped || snapshot() != before {
		t.Fatal("page ranking is unexplained or read generated domain/audit effects", page)
	}
}

func TestRecipeSuggestionsCompleteLotsAndUseByReview(t *testing.T) {
	f, _, _, product, lot := reservationFixture(t)
	catalog, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Date suggestion identity"})
	ingredientBind(t, f, catalog, product)
	rid := suggestionRecipe(t, f, catalog, "Date suggestion", "0.55")
	var past, soon string
	if err := f.pool.QueryRow(f.ctx, "SELECT ((clock_timestamp() AT TIME ZONE timezone)::date-2)::text,((clock_timestamp() AT TIME ZONE timezone)::date+3)::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&past, &soon); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE pantry.lots SET use_by_on=$2::date WHERE id=$1", lot["id"], past); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `WITH lots AS (
 INSERT INTO pantry.lots(household_id,product_id,location_id,received_on,best_before_on,created_by)
 SELECT $1,$2,$3,'2026-01-01',CASE WHEN n=60 THEN $5::date ELSE NULL END,$4 FROM generate_series(1,60) n RETURNING id,household_id)
 INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) SELECT household_id,id,0.01,'receive',$4 FROM lots`, f.identity.HouseholdID, product["id"], lot["location_id"], f.identity.UserID, soon); err != nil {
		t.Fatal(err)
	}
	page, err := f.app.Store.RecipeSuggestions(f.ctx, f.identity, store.RecipeSuggestionQuery{Servings: "1"})
	if err != nil {
		t.Fatal(err)
	}
	got := suggestionCandidate(t, page, rid)
	if got.Outcome != "sufficient" || got.Products[0].Free.Rational != "3/5" || got.DateReview || got.UrgentOn != soon || len(got.Dates) != 1 || got.Dates[0].Free.Rational != "1/100" {
		t.Fatal("incomplete lot sample or unused expired lot changed ranking", got)
	}
	page, err = f.app.Store.RecipeSuggestions(f.ctx, f.identity, store.RecipeSuggestionQuery{Servings: "2"})
	if err != nil {
		t.Fatal(err)
	}
	got = suggestionCandidate(t, page, rid)
	if got.Outcome != "shortage" || !got.DateReview || got.UrgentOn != "" || got.Products[0].Shortage == nil || got.Products[0].Shortage.Rational != "1/2" {
		t.Fatal("passed use-by quantity became available or disappeared without review", got)
	}
	var excluded bool
	for _, reason := range got.Dates {
		if reason.Kind == "use_by" && reason.ExcludedFromCoverage && reason.Free.Rational == "1/2" {
			excluded = true
		}
	}
	if !excluded {
		t.Fatal("uncounted physical quantity lacks explicit exact review evidence", got)
	}
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, f.identity, store.RecipeSuggestionQuery{Servings: "1", PlannedOn: past}); err == nil {
		t.Fatal("past date rehabilitated currently expired stock")
	}
}

func TestRecipeSuggestionsModulePrivacyCurrentReaderAndSession(t *testing.T) {
	f, _, _, product, _ := reservationFixture(t)
	catalog, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Private catalogue identity"})
	ingredientBind(t, f, catalog, product)
	rid := suggestionRecipe(t, f, catalog, "Private suggestion source", "0.5")
	reader := shoppingTestMember(t, f, "suggestion-reader", "reader")
	_, actualReader, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "Suggestion reader")
	if err != nil {
		t.Fatal(err)
	}
	query := store.RecipeSuggestionQuery{Servings: "1"}
	page, err := f.app.Store.RecipeSuggestions(f.ctx, actualReader, query)
	if err != nil || suggestionCandidate(t, page, rid).Outcome != "sufficient" {
		t.Fatal("active reader cannot inspect current suggestions", page, err)
	}
	forged := actualReader
	forged.Role = "admin"
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, forged, query); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("stale/forged role accepted", err)
	}
	state := f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	page, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, query)
	if err != nil {
		t.Fatal(err)
	}
	row := suggestionCandidate(t, page, rid)
	raw, marshalErr := json.Marshal(page)
	if marshalErr != nil || row.Outcome != "incomplete" || len(row.Products) != 0 || row.Lines[0].ProductID != "" || strings.Contains(string(raw), store.String(product["name"])) || strings.Contains(string(raw), store.String(product["id"])) {
		t.Fatal("disabled Pantry metadata leaked or looked sufficient", string(raw), marshalErr)
	}
	state = f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	page, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, query)
	if err != nil || page.RecipesEnabled || len(page.Candidates) != 0 {
		t.Fatal("disabled recipe sources disclosed", page, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", actualReader.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, actualReader, query); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked reader session still reads disabled-state metadata", err)
	}
}

func TestRecipeSuggestionsPageAnchorPrecedesRankingAndBounds(t *testing.T) {
	f, _, _, _, _ := reservationFixture(t)
	for i := range 21 {
		auditCreate(t, f, "recipes", "recipes", store.Record{"title": strings.Repeat("Z", 21-i), "base_servings": "1"})
	}
	query := store.RecipeSuggestionQuery{Servings: "1"}
	first, err := f.app.Store.RecipeSuggestions(f.ctx, f.identity, query)
	if err != nil || len(first.Candidates) != 20 || !first.More || first.AfterCreated.IsZero() || first.AfterID == "" {
		t.Fatal("bounded candidate page missing stable creation anchor", first, err)
	}
	query.AfterCreated, query.AfterID = first.AfterCreated, first.AfterID
	scope := first.Scope()
	query.ExpectedScope, query.CursorExpiresAt = &scope, first.ReadAt.Add(30*time.Minute)
	second, err := f.app.Store.RecipeSuggestions(f.ctx, f.identity, query)
	if err != nil || len(second.Candidates) != 1 || second.More {
		t.Fatal("cursor followed ranked title order instead of immutable creation coordinates", second, err)
	}
	for _, row := range first.Candidates {
		if row.RecipeID == second.Candidates[0].RecipeID || row.Outcome != "empty" {
			t.Fatal("pagination duplicated a recipe or empty recipe looked sufficient", first, second)
		}
	}
	for _, change := range []func(*store.RecipeSuggestionScope){
		func(s *store.RecipeSuggestionScope) { s.Generation++ },
		func(s *store.RecipeSuggestionScope) { s.RecipesGeneration++ },
		func(s *store.RecipeSuggestionScope) { s.PantryGeneration++ },
		func(s *store.RecipeSuggestionScope) { s.PantryEnabled = !s.PantryEnabled },
		func(s *store.RecipeSuggestionScope) { s.RecipesEnabled = !s.RecipesEnabled },
		func(s *store.RecipeSuggestionScope) { s.Timezone = "UTC" },
		func(s *store.RecipeSuggestionScope) { s.Today = "2000-01-01" },
		func(s *store.RecipeSuggestionScope) { s.PlannedOn = "2999-01-01" },
		func(s *store.RecipeSuggestionScope) { s.Search = "changed" },
		func(s *store.RecipeSuggestionScope) { s.Servings = "2.000000" },
	} {
		changed := scope
		change(&changed)
		query.ExpectedScope = &changed
		if _, err = f.app.Store.RecipeSuggestions(f.ctx, f.identity, query); !errors.Is(err, store.ErrConflict) {
			t.Fatal("cursor scope did not match current RR snapshot", changed, err)
		}
	}
	query.ExpectedScope, query.CursorExpiresAt = &scope, first.ReadAt.Add(-time.Second)
	if _, err = f.app.Store.RecipeSuggestions(f.ctx, f.identity, query); !errors.Is(err, store.ErrConflict) {
		t.Fatal("cursor expiry was not checked at the transaction observation time", err)
	}
	for _, bad := range []store.RecipeSuggestionQuery{
		{Servings: "0"}, {Servings: "1.0000001"}, {Servings: "1", Search: "invalid\x00filter"},
		{Servings: "1", Search: strings.Repeat("x", 301)}, {Servings: "1", AfterID: value.UUID()},
		{Servings: "1", AfterCreated: time.Now()},
	} {
		if _, err = f.app.Store.RecipeSuggestions(f.ctx, f.identity, bad); err == nil {
			t.Fatal("invalid input or unpaired cursor accepted", bad)
		}
	}
}

func TestRecipeSuggestionsHTTPBoundCursorPrivacyAndCurrentScope(t *testing.T) {
	f, _, _, _, _ := reservationFixture(t)
	for i := range 21 {
		auditCreate(t, f, "recipes", "recipes", store.Record{"title": "HTTP suggestion " + strings.Repeat("Z", 21-i), "base_servings": "1"})
	}
	get := func(path, token string, status int) suggestionResponse {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, path, token, "", "", nil, nil)
		if err != nil || response.StatusCode != status {
			t.Fatal("suggestion HTTP result", status, string(raw), err)
		}
		var page suggestionResponse
		if status == 200 {
			if response.Header.Get("Cache-Control") != "private, no-store" || json.Unmarshal(raw, &page) != nil {
				t.Fatal("private typed suggestion response", response.Header, string(raw))
			}
		}
		return page
	}
	const path = "/api/v1/planning/suggestions"
	first := get(path+"?servings=1", f.token, 200)
	if first.Next == "" || len(first.Candidates) != 20 {
		t.Fatal("HTTP page missing signed creation anchor", first)
	}
	second := get(path+"?servings=1.000000&cursor="+url.QueryEscape(first.Next), f.token, 200)
	if len(second.Candidates) != 1 || second.Next != "" {
		t.Fatal("equivalent portions failed cursor normalization", second)
	}
	for _, query := range []string{"?servings=2&cursor=", "?q=changed&cursor="} {
		get(path+query+url.QueryEscape(first.Next), f.token, 422)
	}
	get(path+"?cursor="+url.QueryEscape(first.Next+"x"), f.token, 422)
	get(path+"?q=one&q=two", f.token, 422)
	get(path, "", 401)
	otherToken, _, err := f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.1", "Suggestion different session")
	if err != nil {
		t.Fatal(err)
	}
	get(path+"?cursor="+url.QueryEscape(first.Next), otherToken, 422)
	get(path, otherToken, 200)
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.households SET timezone='Pacific/Kiritimati' WHERE id=$1", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	get(path+"?cursor="+url.QueryEscape(first.Next), f.token, 409)
	fresh := get(path, f.token, 200)
	if fresh.Timezone != "Pacific/Kiritimati" {
		t.Fatal("suggestions trusted cached timezone", fresh)
	}
	state := f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	get(path+"?cursor="+url.QueryEscape(fresh.Next), f.token, 409)
	state = f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	get(path, f.token, 404)
}

func TestRecipeSuggestionsTotalIngredientSentinelAndContextRecovery(t *testing.T) {
	f, _, _, _, _ := reservationFixture(t)
	for range 3 {
		recipe, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Large evaluated selection", "base_servings": "1"})
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.ingredients(household_id,recipe_id,position,name,quantity,unit_code,created_by)
 SELECT $1,$2,n,'Bounded ingredient '||n,0.01,'kg',$3 FROM generate_series(1,201) n`, f.identity.HouseholdID, recipe["id"], f.identity.UserID); err != nil {
			t.Fatal(err)
		}
	}
	var invalid store.Invalid
	if _, err := f.app.Store.RecipeSuggestions(f.ctx, f.identity, store.RecipeSuggestionQuery{Servings: "1"}); !errors.As(err, &invalid) {
		t.Fatal("page ingredient total silently truncated", err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err := f.app.Store.RecipeSuggestions(ctx, f.identity, store.RecipeSuggestionQuery{Servings: "1"}); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation was ignored", err)
	}
	if err := f.pool.Ping(f.ctx); err != nil {
		t.Fatal("cancelled read left connection unusable", err)
	}
}
