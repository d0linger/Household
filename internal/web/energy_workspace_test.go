package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/store"
)

func TestEnergyHTTPClosedJSONAndQueries(t *testing.T) {
	const valid = `{"generation":1,"version":0,"action":"meter.save","id":"","data":{"name":"Native meter","unit_code":"kWh"}}`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", valid, true}, {"duplicate root", strings.Replace(valid, `"generation":1`, `"generation":1,"generation":2`, 1), false},
		{"duplicate data", strings.Replace(valid, `"name":"Native meter"`, `"name":"A","name":"B"`, 1), false},
		{"case root", strings.Replace(valid, `"generation"`, `"Generation"`, 1), false},
		{"null data", strings.Replace(valid, `{"name":"Native meter","unit_code":"kWh"}`, `null`, 1), false},
		{"missing version", strings.Replace(valid, `"version":0,`, "", 1), false},
		{"unknown root", strings.Replace(valid, `"version":0`, `"version":0,"sql":"DROP"`, 1), false},
		{"trailing", valid + ` {}`, false}, {"oversize", strings.Replace(valid, "Native meter", strings.Repeat("x", 1<<20), 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/meters/commands", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			var command store.EnergyCommand
			err := readEnergyJSON(httptest.NewRecorder(), r, &command)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	for _, query := range []string{"register=bad", "from=2026-01-01&from=2026-02-01", "search=x&unknown=y", "search=" + strings.Repeat("x", 201)} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/meters/history?"+query, nil)
		if _, err := energyQuery(r); err == nil {
			t.Fatal("ambiguous query accepted", query)
		}
	}
}

func TestEnergyWorkspaceRoutesRemainExplicit(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerEnergyRoutes(mux)
	for _, tc := range []struct{ method, path, pattern string }{
		{"GET", "/m/meters/workspace/capture", "GET /m/meters/workspace/{pane}"},
		{"GET", "/api/v1/meters/series", "GET /api/v1/meters/{view}"},
		{"POST", "/api/v1/meters/commands", "POST /api/v1/meters/commands"},
		{"POST", "/api/v1/meters/readings/private/photo", "POST /api/v1/meters/readings/{id}/photo"},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))
		if pattern != tc.pattern {
			t.Fatal(tc.path, pattern)
		}
	}
}
