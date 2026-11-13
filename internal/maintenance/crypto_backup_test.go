package maintenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"household/internal/config"
	"household/internal/secretbox"
)

func TestSecurityBundleStrictVersionedAndLegacyReaders(t *testing.T) {
	_, secret, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring, err := secretbox.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CSRFSecret: strings.Repeat("c", 32), RateSecret: strings.Repeat("r", 32)}
	raw, err := encodeSecurityBundle(cfg, ring)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeSecurityBundle(raw)
	if err != nil || restored.ActiveID() != ring.ActiveID() {
		t.Fatal("versioned escrow roundtrip", err)
	}
	legacy, err := json.Marshal(map[string]string{"csrf_secret": cfg.CSRFSecret, "rate_secret": cfg.RateSecret, "encryption_key": secret})
	if err != nil {
		t.Fatal(err)
	}
	restored, err = decodeSecurityBundle(legacy)
	if err != nil || restored.LegacyID() != ring.LegacyID() {
		t.Fatal("exact original legacy secret not retained", err)
	}
	for name, bad := range map[string][]byte{
		"duplicate": []byte(`{"version":2,"version":2}`),
		"case":      bytes.Replace(raw, []byte(`"version"`), []byte(`"Version"`), 1),
		"unknown":   bytes.Replace(raw, []byte(`"version":2`), []byte(`"version":2,"extra":1`), 1),
		"trailing":  append(append([]byte(nil), raw...), []byte(` {}`)...),
		"null":      []byte(`null`), "empty": nil,
		"oversized":      bytes.Repeat([]byte(" "), maxSecurityBundle+1),
		"legacy_missing": []byte(`{"csrf_secret":"x","rate_secret":"y"}`),
		"legacy_null":    []byte(`{"csrf_secret":"x","rate_secret":"y","encryption_key":null}`),
		"unsupported":    bytes.Replace(raw, []byte(`"version":2`), []byte(`"version":3`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSecurityBundle(bad); err == nil {
				t.Fatal("unsafe bundle accepted")
			}
		})
	}
}

func TestCryptoMaintenanceRejectsInvalidBatchBeforeConnection(t *testing.T) {
	for _, batch := range []int{-1, 0, 101} {
		if _, err := Reencrypt(t.Context(), nil, config.Config{}, batch); err == nil {
			t.Fatal("invalid batch accepted", batch)
		}
	}
}

func TestSecurityBundleRejectsUnusableSecretsInBothFormats(t *testing.T) {
	_, secret, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring, err := secretbox.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ring.ExportConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, csrf, rate string }{
		{"short csrf", "short", strings.Repeat("r", 32)},
		{"short rate", strings.Repeat("c", 32), "short"},
		{"placeholder", strings.Repeat("c", 32) + "change-me", strings.Repeat("r", 32)},
		{"shared csrf rate", strings.Repeat("c", 32), strings.Repeat("c", 32)},
		{"shared retained encryption", secret, strings.Repeat("r", 32)},
		{"oversized csrf", strings.Repeat("c", secretbox.MaxConfigSize+1), strings.Repeat("r", 32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{CSRFSecret: tc.csrf, RateSecret: tc.rate}
			if _, err := encodeSecurityBundle(cfg, ring); err == nil {
				t.Fatal("backup encoded unusable security configuration")
			}
			for _, format := range []string{"legacy", "versioned"} {
				t.Run(format, func(t *testing.T) {
					var fields any = map[string]string{"csrf_secret": tc.csrf, "rate_secret": tc.rate, "encryption_key": secret}
					if format == "versioned" {
						fields = securityBundle{Version: 2, CSRFSecret: tc.csrf, RateSecret: tc.rate, EncryptionKeys: keys}
					}
					raw, err := json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = decodeSecurityBundle(raw); err == nil {
						t.Fatal("restore accepted keys rejected by application configuration")
					}
				})
			}
		})
	}
	// Valid environment whitespace must remain exact in private escrow.
	cfg := config.Config{CSRFSecret: "  " + strings.Repeat("c", 32) + "\n", RateSecret: "\t" + strings.Repeat("r", 32) + " "}
	raw, err := encodeSecurityBundle(cfg, ring)
	if err != nil {
		t.Fatal(err)
	}
	var bundle securityBundle
	if err = json.Unmarshal(raw, &bundle); err != nil || bundle.CSRFSecret != cfg.CSRFSecret || bundle.RateSecret != cfg.RateSecret {
		t.Fatal("valid environment secret whitespace changed")
	}
	if _, err = decodeSecurityBundle(raw); err != nil {
		t.Fatal("valid versioned whitespace rejected", err)
	}
	legacy, err := json.Marshal(map[string]string{"csrf_secret": cfg.CSRFSecret, "rate_secret": cfg.RateSecret, "encryption_key": secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeSecurityBundle(legacy); err != nil {
		t.Fatal("valid historical whitespace rejected", err)
	}
}
