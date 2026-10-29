package config

import (
	"strings"
	"testing"
)

func TestFoodFactsConfigurationBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		enabled    string
		userAgent  string
		staging    string
		production bool
		wantError  bool
	}{
		{name: "disabled by default"},
		{name: "explicitly disabled", enabled: "false"},
		{name: "enabled but optional contact unconfigured", enabled: "true"},
		{name: "configured", enabled: "true", userAgent: "Household/1.0 (operator@example.invalid)"},
		{name: "development staging", enabled: "true", staging: "true", userAgent: "Household/1.0 (local verification)"},
		{name: "invalid enable flag", enabled: "perhaps", wantError: true},
		{name: "invalid staging flag", staging: "perhaps", wantError: true},
		{name: "staging forbidden in production", staging: "true", production: true, wantError: true},
		{name: "embedded header newline", userAgent: "Household/1.0\r\nInjected: value", wantError: true},
		{name: "embedded control", userAgent: "Household/1.0\x01test", wantError: true},
		{name: "non ASCII header", userAgent: "Household/1.0 (Küche)", wantError: true},
		{name: "oversized header", userAgent: strings.Repeat("x", 257), wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("HOUSEHOLD_OFF_ENABLED", tc.enabled)
			t.Setenv("HOUSEHOLD_OFF_USER_AGENT", tc.userAgent)
			t.Setenv("HOUSEHOLD_OFF_STAGING", tc.staging)
			if tc.production {
				t.Setenv("HOUSEHOLD_ENV", "production")
				t.Setenv("HOUSEHOLD_ORIGIN", "https://household.example.invalid")
			}
			cfg, err := Load()
			if (err != nil) != tc.wantError {
				t.Fatalf("Load error=%v; want error=%v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			if cfg.FoodFactsEnabled != (tc.enabled == "true") ||
				cfg.FoodFactsStaging != (tc.staging == "true") || cfg.FoodFactsUserAgent != tc.userAgent {
				t.Fatal("optional source configuration differs from explicit settings")
			}
		})
	}
}
