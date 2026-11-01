package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/store"
)

func TestChoreClosedJSONRejectsAmbiguousAndOversizedCommands(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"generation":1,"version":0,"name":"Küche","position":0,"archived":false}`, true},
		{"duplicate", `{"generation":1,"version":0,"name":"A","name":"B"}`, false},
		{"case", `{"generation":1,"version":0,"Name":"A"}`, false},
		{"unknown", `{"generation":1,"version":0,"sql":"DELETE"}`, false},
		{"missing-generation", `{"version":0,"name":"A"}`, false},
		{"null-version", `{"generation":1,"version":null,"name":"A"}`, false},
		{"trailing", `{"generation":1,"version":0} {}`, false},
		{"oversize", `{"generation":1,"version":0,"name":"` + strings.Repeat("a", 64<<10) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/chores/areas", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			var command store.ChoreAreaInput
			err := readChoreJSON(httptest.NewRecorder(), request, &command)
			if (err == nil) != tc.valid {
				t.Fatal("closed JSON result", err)
			}
		})
	}
}

func TestChoreRoutesPreserveCalendarSecretInPatternOnly(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerChoreRoutes(mux)
	for _, tc := range []struct{ method, path, pattern string }{{"GET", "/calendar/chores/private.ics", "GET /calendar/chores/{token}"}, {"GET", "/api/v1/chores/series/abc", "GET /api/v1/chores/series/{id}"}, {"POST", "/api/v1/chores/series/preview", "POST /api/v1/chores/series/preview"}, {"GET", "/m/chores/workspace/series/new", "GET /m/chores/workspace/series/{id}"}} {
		_, pattern := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))
		if pattern != tc.pattern {
			t.Fatalf("route %s resolved as %s", tc.path, pattern)
		}
	}
}
