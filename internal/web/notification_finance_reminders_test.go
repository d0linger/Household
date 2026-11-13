package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/store"
)

func TestFinancePaymentReminderClosedJSON(t *testing.T) {
	valid := `{"action":"save","version":0,"entry_version":3,"generation":1,"access_version":"` + strings.Repeat("a", 64) + `","due_on":"2026-11-20","responsible_member_id":"11111111-1111-4111-8111-111111111111","request_id":"22222222-2222-4222-8222-222222222222"}`
	for _, tc := range []struct {
		name, body, media, query string
		valid                    bool
	}{
		{"complete command", valid, "application/json", "", true},
		{"charset", valid, "application/json; charset=utf-8", "", true},
		{"missing key", strings.Replace(valid, `"version":0,`, "", 1), "application/json", "", false},
		{"null version", strings.Replace(valid, `"version":0`, `"version":null`, 1), "application/json", "", false},
		{"null responsible", strings.Replace(valid, `"responsible_member_id":"11111111-1111-4111-8111-111111111111"`, `"responsible_member_id":null`, 1), "application/json", "", false},
		{"duplicate", strings.Replace(valid, `"version":0`, `"version":0,"version":2`, 1), "application/json", "", false},
		{"escaped duplicate", strings.Replace(valid, `"version":0`, `"version":0,"vers\u0069on":2`, 1), "application/json", "", false},
		{"case alias", strings.Replace(valid, `"version":0`, `"Version":0`, 1), "application/json", "", false},
		{"unknown payment", strings.Replace(valid, `"version":0`, `"version":0,"paid":true`, 1), "application/json", "", false},
		{"fractional version", strings.Replace(valid, `"version":0`, `"version":0.5`, 1), "application/json", "", false},
		{"numeric string", strings.Replace(valid, `"version":0`, `"version":"0"`, 1), "application/json", "", false},
		{"array", "[" + valid + "]", "application/json", "", false},
		{"trailing", valid + `{}`, "application/json", "", false},
		{"invalid UTF8", strings.Replace(valid, "save", "sa\xffve", 1), "application/json", "", false},
		{"oversize", strings.Replace(valid, "save", strings.Repeat("x", 4096), 1), "application/json", "", false},
		{"wrong media", valid, "text/plain", "", false},
		{"query", valid, "application/json", "?paid=true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/expenses/finance/entries/id/payment-reminder"+tc.query, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			var in store.FinancePaymentReminderInput
			err := readFinancePaymentReminderJSON(httptest.NewRecorder(), r, &in)
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v, want %v", err == nil, tc.valid)
			}
			if tc.valid && (in.Action != "save" || in.Version != 0 || in.EntryVersion != 3 || in.Generation != 1) {
				t.Fatal("command fields changed during decoding")
			}
		})
	}
}
