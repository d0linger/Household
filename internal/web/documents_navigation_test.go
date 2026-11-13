package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentPageQuerySeparatesReturnContext(t *testing.T) {
	for _, pane := range []string{"inbox", "library", "deadlines", "processing", "duplicates"} {
		t.Run(pane, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?search=invoice&from="+pane, nil)
			r.SetPathValue("pane", "review")
			original := r.URL.RawQuery
			q, origin, err := documentPageQuery(r)
			if err != nil || origin != pane || q.Get("search") != "invoice" || q.Has("from") || r.URL.RawQuery != original {
				t.Fatalf("query=%v origin=%q err=%v original=%q", q, origin, err, r.URL.RawQuery)
			}
			if _, err := documentQuery(r); err == nil {
				t.Fatal("business API accepted page-only navigation context")
			}
		})
	}
	r := httptest.NewRequest("GET", "/?search=invoice", nil)
	r.SetPathValue("pane", "review")
	if _, origin, err := documentPageQuery(r); err != nil || origin != "library" {
		t.Fatalf("default origin=%q err=%v", origin, err)
	}
}

func TestDocumentPageQueryRejectsInvalidReturnContext(t *testing.T) {
	for _, tc := range []struct{ pane, query string }{
		{"review", "from="}, {"review", "from=rules"}, {"review", "from=https://example.com"},
		{"review", "from=inbox&from=library"}, {"inbox", "from=library"},
		{"review", "from=library&search=a&search=b"}, {"review", "from=library&unknown=x"},
		{"review", "from=%zz"}, {"review", "search=" + strings.Repeat("x", 6001)},
	} {
		r := httptest.NewRequest("GET", "/?"+tc.query, nil)
		r.SetPathValue("pane", tc.pane)
		if _, _, err := documentPageQuery(r); err == nil {
			t.Errorf("accepted pane %q query %q", tc.pane, tc.query)
		}
	}
}
