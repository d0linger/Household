package pwned

import (
	"context"
	"crypto/sha1" // #nosec G505 -- test of the HIBP range protocol
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCountSendsOnlyThePrefix(t *testing.T) {
	sum := sha1.Sum([]byte("correct horse battery staple")) // #nosec G401 -- protocol test
	digest := strings.ToUpper(hex.EncodeToString(sum[:]))
	var requested, padding string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested, padding = r.URL.Path, r.Header.Get("Add-Padding")
		_, _ = w.Write([]byte("0000000000000000000000000000000000A:0\r\n" + digest[5:] + ":42\r\nFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF:3\r\n"))
	}))
	defer server.Close()
	old := Endpoint
	Endpoint = server.URL + "/range/"
	t.Cleanup(func() { Endpoint = old })
	checker := New()
	checker.Client = server.Client()
	n, err := checker.Count(context.Background(), "correct horse battery staple")
	if err != nil || n != 42 {
		t.Fatalf("count = %d %v", n, err)
	}
	if requested != "/range/"+digest[:5] || strings.Contains(requested, digest[5:]) || padding != "true" {
		t.Fatalf("request path %q padding %q", requested, padding)
	}
	if n, err = checker.Count(context.Background(), "another long passphrase here"); err != nil || n != 0 {
		t.Fatalf("unknown password = %d %v", n, err)
	}
}

func TestCountFailsOpenAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	old := Endpoint
	Endpoint = server.URL + "/range/"
	t.Cleanup(func() { Endpoint = old })
	checker := New()
	checker.Client = server.Client()
	if _, err := checker.Count(context.Background(), "x"); err != ErrUnavailable {
		t.Fatalf("err = %v", err)
	}
}
