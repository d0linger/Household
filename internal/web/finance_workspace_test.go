package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
)

func financeProofRequest(id auth.Identity) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/expenses/finance/view?pane=entries", nil)
	return r.WithContext(context.WithValue(r.Context(), identityKey, id))
}

func TestFinanceCursorBindsWholeCurrentAuthorityAndSelection(t *testing.T) {
	service, err := auth.New(nil, config.Config{CSRFSecret: strings.Repeat("synthetic-finance-cursor", 3), EncryptionKey: strings.Repeat("e", 32)})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Auth: service}
	id := auth.Identity{HouseholdID: "household", UserID: "user", MembershipID: "member", SessionID: "session", Role: "editor", AuthVersion: 7}
	r := financeProofRequest(id)
	binding := `view:{"Pane":"entries","Month":"2026-10"}`
	token := s.financeSignCursorMonth(r, "raw-keyset", binding, "2026-10")
	if got, e := s.financeReadCursor(r, token, binding); e != nil || got != "raw-keyset" || financeUnverifiedCursorMonth(token) != "2026-10" {
		t.Fatal(got, e)
	}
	for _, change := range []func(*auth.Identity){
		func(i *auth.Identity) { i.HouseholdID = "other" }, func(i *auth.Identity) { i.UserID = "other" },
		func(i *auth.Identity) { i.MembershipID = "other" }, func(i *auth.Identity) { i.SessionID = "other" },
		func(i *auth.Identity) { i.Role = "reader" }, func(i *auth.Identity) { i.AuthVersion++ },
	} {
		changed := id
		change(&changed)
		if _, e := s.financeReadCursor(financeProofRequest(changed), token, binding); e == nil {
			t.Fatal("cursor accepted different authority", changed)
		}
	}
	for _, other := range []string{"refs:accounts", binding + "other", strings.Replace(binding, "2026-10", "2026-09", 1)} {
		if _, e := s.financeReadCursor(r, token, other); e == nil {
			t.Fatal("cursor accepted another selection", other)
		}
	}
	for _, bad := range []string{"forged.signature", token + "x", strings.Repeat("x", 4097)} {
		if _, e := s.financeReadCursor(r, bad, binding); e == nil {
			t.Fatal("malformed cursor accepted")
		}
	}
	// An attacker can alter the candidate month, but the full envelope must still
	// authenticate against the corresponding complete query before any SQL read.
	payload, signature, _ := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	changedPayload := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), "2026-10", "2026-09", 1)))
	forged := changedPayload + "." + signature
	if financeUnverifiedCursorMonth(forged) != "2026-09" {
		t.Fatal("candidate extraction changed")
	}
	if _, e := s.financeReadCursor(r, forged, strings.Replace(binding, "2026-10", "2026-09", 1)); e == nil {
		t.Fatal("unverified month became trusted")
	}
	proof, err := json.Marshal(financeCursorEnvelope{Raw: "key", Expires: time.Now().Add(-time.Second).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	payload = base64.RawURLEncoding.EncodeToString(proof)
	expired := payload + "." + service.CSRF(payload, "finance-cursor:"+financeScope(r)+binding)
	if _, e := s.financeReadCursor(r, expired, binding); e == nil {
		t.Fatal("expired cursor accepted")
	}
}

func TestFinanceJSONRequiresSingleBoundedJSONRequest(t *testing.T) {
	for _, tc := range []struct {
		media, target, body string
		ok                  bool
	}{
		{"application/json", "/api/v1/expenses/finance/preview", `{}`, true},
		{"application/json; charset=utf-8", "/api/v1/expenses/finance/preview", `{}`, true},
		{"text/plain", "/api/v1/expenses/finance/preview", `{}`, false},
		{"application/json", "/api/v1/expenses/finance/preview?generation=1", `{}`, false},
		{"application/json", "/api/v1/expenses/finance/preview", strings.Repeat("x", 1<<20+1), false},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.media)
		raw, err := financeJSON(httptest.NewRecorder(), r)
		if (err == nil) != tc.ok || tc.ok && string(raw) != tc.body {
			t.Fatal(tc.media, tc.target, len(tc.body), err)
		}
	}
}

func TestFinanceHTTPQueryRejectsUnknownDuplicateAndMalformedFields(t *testing.T) {
	for _, query := range []string{"pane=entries&pane=accounts", "pane=entries&unknown=true", "pane=entries&payee=%zz", "pane=entries&" + strings.Repeat("x", 12001)} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/expenses/finance/view?"+query, nil)
		if _, err := financeQuery(r); err == nil {
			t.Fatal("ambiguous HTTP query accepted", query[:min(len(query), 60)])
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/expenses/finance/view?pane=planning&section=contributions&goal=00000000-0000-4000-8000-000000000001&month=2026-10&payee=Markt%20Nord", nil)
	q, err := financeQuery(r)
	if err != nil || q.Goal != "00000000-0000-4000-8000-000000000001" || q.Payee != "Markt Nord" || q.Month != "2026-10" {
		t.Fatal(q, err)
	}
}
