package secretbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSecretboxStrictConfig(t *testing.T) {
	ring, err := New(testSecretA)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := ring.ExportConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := string(valid)
	for _, tc := range []struct{ name, raw string }{
		{"empty", ""}, {"null", "null"}, {"array", "[]"}, {"trailing", base + " {}"},
		{"unknown", strings.Replace(base, `"version":1`, `"unknown":true,"version":1`, 1)},
		{"case alias", strings.Replace(base, `"active":`, `"Active":`, 1)},
		{"duplicate version", strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1)},
		{"escaped duplicate", strings.Replace(base, `"version":1`, `"version":1,"\u0076ersion":1`, 1)},
		{"duplicate entry field", strings.Replace(base, `"secret":`, `"secret":"another synthetic sufficiently long key","secret":`, 1)},
		{"null keys", strings.Replace(base, `"keys":[`, `"keys":null,"ignored":[`, 1)},
		{"null legacy", strings.Replace(base, `"legacy":"`+ring.LegacyID()+`"`, `"legacy":null`, 1)},
		{"empty legacy", strings.Replace(base, `"legacy":"`+ring.LegacyID()+`"`, `"legacy":""`, 1)},
		{"fractional version", strings.Replace(base, `"version":1`, `"version":1.0`, 1)},
		{"unknown version", strings.Replace(base, `"version":1`, `"version":2`, 1)},
		{"wrong fingerprint", strings.ReplaceAll(base, ring.ActiveID(), strings.Repeat("0", 64))},
		{"missing active", strings.Replace(base, `"active":"`+ring.ActiveID()+`",`, ``, 1)},
		{"invalid UTF8", base + string([]byte{255})},
		{"too large", strings.Repeat(" ", MaxConfigSize) + base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tc.raw)); err == nil {
				t.Fatal("invalid keyring accepted")
			}
		})
	}
	for _, secret := range []string{"", strings.Repeat(" ", 32), strings.Repeat("x", 31), strings.Repeat("x", 4097), "change-me-" + strings.Repeat("x", 32)} {
		if _, err := New(secret); err == nil {
			t.Fatal("invalid secret accepted")
		}
	}
	var cfg ringConfig
	if err := json.Unmarshal(valid, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Keys = append(cfg.Keys, cfg.Keys[0])
	duplicate, _ := json.Marshal(cfg)
	if _, err := ParseConfig(duplicate); err == nil {
		t.Fatal("duplicate keys accepted")
	}
	cfg.Keys = nil
	for i := range MaxKeys + 1 {
		secret := strings.Repeat(string(rune('a'+i)), 32)
		cfg.Keys = append(cfg.Keys, configEntry{ID: fingerprint(secret), Secret: secret})
	}
	cfg.Active = cfg.Keys[0].ID
	cfg.Legacy = ""
	many, _ := json.Marshal(cfg)
	if _, err := ParseConfig(many); err == nil {
		t.Fatal("unbounded keyring accepted")
	}
	cfg.Keys = cfg.Keys[:MaxKeys]
	bounded, _ := json.Marshal(cfg)
	if _, err := ParseConfig(bounded); err != nil {
		t.Fatal("maximum supported keyring rejected", err)
	}
}
