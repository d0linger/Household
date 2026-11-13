package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotificationPreferenceJSONOptionalQuietHours(t *testing.T) {
	const valid = `{"category":"test","enabled":false,"timezone":"UTC","quiet_start":null,"quiet_end":null,"version":1}`
	for _, tc := range []struct {
		name, body, media string
		valid             bool
	}{
		{"cleared quiet hours", valid, "application/json", true},
		{"active quiet hours", strings.ReplaceAll(valid, "null", "60"), "application/json; charset=utf-8", true},
		{"missing key", strings.Replace(valid, `,"quiet_start":null`, "", 1), "application/json", false},
		{"null enabled", strings.Replace(valid, "false", "null", 1), "application/json", false},
		{"null category", strings.Replace(valid, `"test"`, "null", 1), "application/json", false},
		{"null version", strings.Replace(valid, `"version":1`, `"version":null`, 1), "application/json", false},
		{"null timezone", strings.Replace(valid, `"UTC"`, "null", 1), "application/json", false},
		{"duplicate key", strings.Replace(valid, `"version":1`, `"version":1,"version":2`, 1), "application/json", false},
		{"escaped duplicate", strings.Replace(valid, `"version":1`, `"version":1,"\u0076ersion":2`, 1), "application/json", false},
		{"unknown key", strings.Replace(valid, `"version":1`, `"version":1,"url":"/logout"`, 1), "application/json", false},
		{"wrong case", strings.Replace(valid, `"version"`, `"Version"`, 1), "application/json", false},
		{"fractional minute", strings.Replace(valid, `"quiet_start":null`, `"quiet_start":1.5`, 1), "application/json", false},
		{"nested value", strings.Replace(valid, `"quiet_start":null`, `"quiet_start":{}`, 1), "application/json", false},
		{"second document", valid + `{}`, "application/json", false},
		{"truncated", valid[:len(valid)-1], "application/json", false},
		{"wrong media", valid, "text/plain", false},
		{"oversize", strings.Replace(valid, "UTC", strings.Repeat("a", 2048), 1), "application/json", false},
		{"invalid UTF8", strings.Replace(valid, "UTC", "\xff", 1), "application/json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/core/notification-preferences", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			var input notificationPreferenceInput
			err := readNotificationPreferenceJSON(httptest.NewRecorder(), r, &input)
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v want=%v: %v", err == nil, tc.valid, err)
			}
			if tc.name == "cleared quiet hours" && (input.Start != nil || input.End != nil) {
				t.Fatal("clearing quiet hours did not preserve null")
			}
		})
	}
}
