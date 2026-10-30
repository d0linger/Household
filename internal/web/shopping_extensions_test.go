package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/webpush"
)

func TestShoppingExtensionGenerationRequiresOneCanonicalSafeInteger(t *testing.T) {
	for _, raw := range []string{"generation=1", "generation=9007199254740991"} {
		if _, err := shoppingExtensionGeneration(raw); err != nil {
			t.Fatalf("valid generation rejected: %s", raw)
		}
	}
	for _, raw := range []string{"", "generation=0", "generation=-1", "generation=01", "generation=%2B1", "generation=1.0", "generation=9007199254740992", "generation=1&generation=1", "generation=1&other=1", "generation=%zz"} {
		if _, err := shoppingExtensionGeneration(raw); err == nil {
			t.Fatalf("noncanonical generation accepted: %s", raw)
		}
	}
}

func TestShoppingClosedJSONRejectsAmbiguityAndBounds(t *testing.T) {
	cases := []struct {
		name, body, contentType string
		limit                   int64
		wantError               bool
	}{
		{name: "closed object", body: `{"generation":1,"revision":"0","enabled":true,"push_enabled":false,"mute_for_seconds":0}`},
		{name: "duplicate", body: `{"enabled":true,"enabled":false}`, wantError: true},
		{name: "escaped duplicate", body: `{"enabled":true,"\u0065nabled":false}`, wantError: true},
		{name: "wrong case", body: `{"Enabled":true}`, wantError: true},
		{name: "unknown", body: `{"user_id":"foreign"}`, wantError: true},
		{name: "trailing", body: `{} {}`, wantError: true},
		{name: "null", body: `null`, wantError: true},
		{name: "array", body: `[]`, wantError: true},
		{name: "wrong scalar type", body: `{"generation":"1"}`, wantError: true},
		{name: "missing boolean", body: `{"generation":1,"revision":"0","enabled":true,"mute_for_seconds":0}`, wantError: true},
		{name: "null boolean", body: `{"generation":1,"revision":"0","enabled":true,"push_enabled":null,"mute_for_seconds":0}`, wantError: true},
		{name: "oversized", body: `{"revision":"` + strings.Repeat("1", 200) + `"}`, limit: 100, wantError: true},
		{name: "wrong media", body: `{}`, contentType: "text/plain", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("PUT", "/api/v1/shopping/lists/example/notification-preferences", strings.NewReader(tc.body))
			contentType := tc.contentType
			if contentType == "" {
				contentType = "application/json; charset=utf-8"
			}
			request.Header.Set("Content-Type", contentType)
			limit := tc.limit
			if limit == 0 {
				limit = 2048
			}
			var input store.ShoppingPreferenceInput
			err := readShoppingExtensionJSON(httptest.NewRecorder(), request, &input, limit)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v; expected error=%v", err, tc.wantError)
			}
		})
	}
}

func TestShoppingPushJSONRejectsNestedUnknownAndDuplicateKeys(t *testing.T) {
	for _, body := range []string{
		`{"subscription":{"endpoint":"first","endpoint":"second"}}`,
		`{"subscription":{"keys":{"auth":"a","Auth":"b"}}}`,
		`{"subscription":{"keys":{"auth":"a","private_key":"b"}}}`,
		`{"subscription":[]}`,
	} {
		r := httptest.NewRequest("POST", "/api/v1/core/push/subscriptions", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var input webpush.RegisterInput
		if readShoppingExtensionJSON(httptest.NewRecorder(), r, &input, 8192) == nil {
			t.Fatal("ambiguous or unknown nested receiver data accepted")
		}
	}
}
