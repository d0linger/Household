package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/store"
	"household/internal/value"
)

func TestSuggestionRequestCanonicalAndBounded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"", true}, {"q=%20Soup%20&servings=2.5&on=2026-12-01", true},
		{"servings=0", false}, {"servings=1.0000001", false}, {"servings=NaN", false},
		{"on=2026-02-30", false}, {"q=one&q=two", false}, {"cursor=a&cursor=b", false},
		{"after_id=untrusted", false}, {"q=%zz", false}, {"q=a;b", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			query, _, err := suggestionRequest(httptest.NewRequest("GET", "/?"+tc.query, nil))
			if (err == nil) != tc.valid {
				t.Fatal(query, err)
			}
			if err == nil && query.Servings != "1.000000" && (query.Servings != "2.500000" || query.Search != "Soup") {
				t.Fatal("request was not normalized", query)
			}
		})
	}
}

func TestSuggestionCursorBindsIdentityRequestAndStrictPayload(t *testing.T) {
	t.Parallel()
	service, err := auth.New(nil, config.Config{CSRFSecret: strings.Repeat("s", 32), EncryptionKey: strings.Repeat("e", 32)})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Auth: service}
	id := auth.Identity{HouseholdID: value.UUID(), UserID: value.UUID(), MembershipID: value.UUID(), SessionID: value.UUID(), Role: "reader", AuthVersion: 1}
	query := store.RecipeSuggestionQuery{Search: "Soup", Servings: "1.000000", PlannedOn: "2026-12-01"}
	scope, err := suggestionRequestScope(id, query)
	if err != nil {
		t.Fatal(err)
	}
	cursor := suggestionCursor{Version: 1, Created: time.Now().UTC(), ID: value.UUID(), Expires: time.Now().Add(time.Minute).Unix(),
		Scope: store.RecipeSuggestionScope{Generation: 1, RecipesGeneration: 2, PantryGeneration: 3, RecipesEnabled: true, PantryEnabled: true,
			Timezone: "Europe/Vienna", Today: "2026-10-26", Search: query.Search, Servings: query.Servings, PlannedOn: query.PlannedOn}}
	token, err := s.makeSuggestionCursor(cursor, scope)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.readSuggestionCursor(token, scope)
	if err != nil || got.ID != cursor.ID || !got.Created.Equal(cursor.Created) || got.Scope != cursor.Scope {
		t.Fatal("own bound cursor rejected", got, err)
	}
	for _, change := range []func(*auth.Identity, *store.RecipeSuggestionQuery){
		func(i *auth.Identity, _ *store.RecipeSuggestionQuery) { i.HouseholdID = value.UUID() },
		func(i *auth.Identity, _ *store.RecipeSuggestionQuery) { i.UserID = value.UUID() },
		func(i *auth.Identity, _ *store.RecipeSuggestionQuery) { i.MembershipID = value.UUID() },
		func(i *auth.Identity, _ *store.RecipeSuggestionQuery) { i.SessionID = value.UUID() },
		func(i *auth.Identity, _ *store.RecipeSuggestionQuery) { i.Role = "admin" },
		func(i *auth.Identity, _ *store.RecipeSuggestionQuery) { i.AuthVersion++ },
		func(_ *auth.Identity, q *store.RecipeSuggestionQuery) { q.Search += "x" },
		func(_ *auth.Identity, q *store.RecipeSuggestionQuery) { q.Servings = "2.000000" },
	} {
		otherID, otherQuery := id, query
		change(&otherID, &otherQuery)
		otherScope, scopeErr := suggestionRequestScope(otherID, otherQuery)
		if scopeErr != nil {
			t.Fatal(scopeErr)
		}
		if _, err = s.readSuggestionCursor(token, otherScope); err == nil {
			t.Fatal("cursor accepted changed authorization/filter scope")
		}
	}
	for _, bad := range []string{"", token + "x", strings.Repeat("x", 4097), "unbound.signature"} {
		if _, err = s.readSuggestionCursor(bad, scope); err == nil {
			t.Fatal("malformed cursor accepted")
		}
	}
	for _, change := range []func(*suggestionCursor){
		func(c *suggestionCursor) { c.Version = 2 },
		func(c *suggestionCursor) { c.Expires = time.Now().Add(-time.Minute).Unix() },
		func(c *suggestionCursor) { c.ID = "invalid" },
		func(c *suggestionCursor) { c.Created = time.Time{} },
		func(c *suggestionCursor) { c.Scope.Generation = 0 },
		func(c *suggestionCursor) { c.Scope.Today = "2026-02-30" },
	} {
		bad := cursor
		change(&bad)
		badToken, signErr := s.makeSuggestionCursor(bad, scope)
		if signErr != nil {
			t.Fatal(signErr)
		}
		if _, err = s.readSuggestionCursor(badToken, scope); err == nil {
			t.Fatal("signed invalid payload accepted", bad)
		}
	}
}
