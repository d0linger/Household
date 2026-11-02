package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/store"
)

func TestDocumentJSONRejectsDuplicatesAndUnknownRoot(t *testing.T) {
	valid := `{"generation":1,"version":0,"action":"save","id":"","data":{"title":"Invoice","fields":{"reference":"local"}}}`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid object", valid, true}, {"duplicate root", strings.Replace(valid, `"version":0`, `"version":0,"version":1`, 1), false},
		{"nested duplicate", strings.Replace(valid, `"title":"Invoice"`, `"title":"Invoice","title":"Other"`, 1), false},
		{"unknown root", strings.Replace(valid, `"version":0`, `"version":0,"sql":"x"`, 1), false},
		{"trailing", valid + `{}`, false}, {"missing generation", strings.Replace(valid, `"generation":1,`, "", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/documents/commands", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			var in store.DocumentCommand
			err := readDocumentJSON(httptest.NewRecorder(), r, &in)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestDocumentRoutesKeepArchiveAndUploadsExplicit(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerDocumentRoutes(mux)
	for _, tc := range []struct{ method, path, pattern string }{
		{"GET", "/m/documents/workspace/inbox", "GET /m/documents/workspace/{pane}"},
		{"GET", "/api/v1/documents/export.zip", "GET /api/v1/documents/export.zip"},
		{"POST", "/api/v1/documents/file/original", "POST /api/v1/documents/{id}/original"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, actual := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))
			if actual != tc.pattern {
				t.Fatal(actual)
			}
		})
	}
}

func TestDocumentOriginalNameKeepsUnicodeAndExtension(t *testing.T) {
	input := "abc" + strings.Repeat("ö", 200) + ".png"
	actual := documentOriginalName(input)
	if len([]rune(actual)) != 200 || !strings.HasSuffix(actual, ".png") || strings.ContainsRune(actual, '�') {
		t.Fatal(actual)
	}
}
