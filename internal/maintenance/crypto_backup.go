package maintenance

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"household/internal/config"
	"household/internal/secretbox"
)

const maxSecurityBundle = 64 << 10

type securityBundle struct {
	Version        int             `json:"version"`
	CSRFSecret     string          `json:"csrf_secret"`
	RateSecret     string          `json:"rate_secret"`
	EncryptionKeys json.RawMessage `json:"encryption_keys"`
}

func encodeSecurityBundle(cfg config.Config, ring *secretbox.Keyring) ([]byte, error) {
	if err := validateSecurityBundleSecrets(ring, cfg.CSRFSecret, cfg.RateSecret); err != nil {
		return nil, err
	}
	keys, err := ring.ExportConfig()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(securityBundle{Version: 2, CSRFSecret: cfg.CSRFSecret, RateSecret: cfg.RateSecret, EncryptionKeys: keys})
	if err != nil || len(raw) > maxSecurityBundle {
		return nil, errors.New("backup security bundle exceeds bounds")
	}
	return raw, nil
}

func decodeSecurityBundle(raw []byte) (*secretbox.Keyring, error) {
	if len(raw) == 0 || len(raw) > maxSecurityBundle {
		return nil, errors.New("invalid backup security bundle size")
	}
	var fields map[string]json.RawMessage
	if err := strictPrivateJSON(raw, &fields); err != nil {
		return nil, errors.New("invalid backup security bundle")
	}
	if fields["version"] == nil {
		if len(fields) != 3 || fields["csrf_secret"] == nil || fields["encryption_key"] == nil || fields["rate_secret"] == nil {
			return nil, errors.New("invalid legacy backup security bundle fields")
		}
		var legacy struct {
			CSRF       string `json:"csrf_secret"`
			Encryption string `json:"encryption_key"`
			Rate       string `json:"rate_secret"`
		}
		if err := strictPrivateJSON(raw, &legacy); err != nil || legacy.CSRF == "" || legacy.Rate == "" || legacy.Encryption == "" {
			return nil, errors.New("incomplete legacy backup security bundle")
		}
		ring, err := secretbox.New(legacy.Encryption)
		if err != nil {
			return nil, err
		}
		if err = validateSecurityBundleSecrets(ring, legacy.CSRF, legacy.Rate); err != nil {
			return nil, err
		}
		return ring, nil
	}
	if len(fields) != 4 || fields["csrf_secret"] == nil || fields["rate_secret"] == nil || fields["encryption_keys"] == nil {
		return nil, errors.New("invalid versioned backup security bundle fields")
	}
	var bundle securityBundle
	if err := strictPrivateJSON(raw, &bundle); err != nil || bundle.Version != 2 || bundle.CSRFSecret == "" || bundle.RateSecret == "" {
		return nil, errors.New("unsupported or incomplete backup security bundle")
	}
	ring, err := secretbox.ParseConfig(bundle.EncryptionKeys)
	if err != nil {
		return nil, err
	}
	if err = validateSecurityBundleSecrets(ring, bundle.CSRFSecret, bundle.RateSecret); err != nil {
		return nil, err
	}
	return ring, nil
}

func validateSecurityBundleSecrets(ring *secretbox.Keyring, csrf, rate string) error {
	// Match config.Load's bounded CSRF/rate acceptance without normalizing the
	// supplied strings: legacy environment whitespace is part of key material.
	for _, value := range []string{csrf, rate} {
		if len(strings.TrimSpace(value)) < 32 || len(value) > secretbox.MaxConfigSize || strings.Contains(strings.ToLower(value), "change-me") {
			return errors.New("backup security secrets are invalid")
		}
	}
	if err := ring.ValidateIndependent(csrf, rate); err != nil {
		return errors.New("backup security secrets must be independent")
	}
	return nil
}

func readSecurityBundle(path string) (*secretbox.Keyring, error) {
	file, err := os.Open(path) // #nosec G304 G703 -- fixed security-keys.json inside the owned private restore directory; unique manifest/dump validation and symlink rejection precede this bounded read.
	if err != nil {
		return nil, errors.New("restored security bundle missing")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxSecurityBundle {
		return nil, errors.New("invalid restored security bundle file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSecurityBundle+1))
	if err != nil {
		return nil, errors.New("read restored security bundle failed")
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	return decodeSecurityBundle(raw)
}
