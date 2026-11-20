package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckComparesTheRunningRevisionOncePerInterval(t *testing.T) {
	main := strings.Repeat("b", 40)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("User-Agent") != "Household-update-check" {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`{"sha":"` + main + `","commit":{"message":"x"}}`))
	}))
	defer server.Close()
	old := Endpoint
	Endpoint = server.URL
	t.Cleanup(func() { Endpoint = old })
	running := strings.Repeat("a", 40)
	checker := New(func() string { return running })
	checker.Client = server.Client()
	now := time.Now()
	if result := checker.Check(context.Background(), now); !result.Available || result.Latest != main || result.Failed {
		t.Fatalf("result = %+v", result)
	}
	checker.Check(context.Background(), now.Add(time.Hour))
	if calls.Load() != 1 {
		t.Fatalf("lookups within the interval = %d", calls.Load())
	}
	running = main
	if result := checker.Check(context.Background(), now.Add(25*time.Hour)); result.Available {
		t.Fatal("current build reported as outdated")
	}
	running = "unknown"
	if result := checker.Check(context.Background(), now.Add(50*time.Hour)); result.Available {
		t.Fatal("local build reported as outdated")
	}
	// Short build revisions compare with the same prefix of the latest commit.
	running = main[:12]
	if result := checker.Check(context.Background(), now.Add(75*time.Hour)); result.Available {
		t.Fatal("matching short revision reported as outdated")
	}
	running = strings.Repeat("c", 7)
	if result := checker.Check(context.Background(), now.Add(100*time.Hour)); !result.Available {
		t.Fatal("different short revision not reported")
	}
}

func TestCheckReportsFailureWithoutAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"sha":"not-a-commit"}`)) }))
	defer server.Close()
	old := Endpoint
	Endpoint = server.URL
	t.Cleanup(func() { Endpoint = old })
	checker := New(func() string { return strings.Repeat("a", 40) })
	checker.Client = server.Client()
	if result := checker.Check(context.Background(), time.Now()); !result.Failed || result.Available {
		t.Fatalf("result = %+v", result)
	}
}
