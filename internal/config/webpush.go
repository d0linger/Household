package config

import (
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"io"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func loadWebPush(c *Config) error {
	if raw := os.Getenv("HOUSEHOLD_WEB_PUSH_ENABLED"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return errors.New("invalid web push setting")
		}
		c.WebPushEnabled = enabled
	}
	c.WebPushContact = strings.TrimSpace(os.Getenv("HOUSEHOLD_WEB_PUSH_VAPID_CONTACT"))
	if c.WebPushContact != "" && !validWebPushContact(c.WebPushContact) {
		return errors.New("web push contact requires a bounded mailto or HTTPS URI")
	}
	private, file := os.Getenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY"), os.Getenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY_FILE")
	if private != "" && file != "" {
		return errors.New("both web push private key and file are configured")
	}
	if file != "" {
		// security: this bounded secret path comes only from operator configuration.
		reader, err := os.Open(file) // #nosec G304 G703 -- trusted deployment secret path
		if err != nil {
			return errors.New("web push private key file could not be read")
		}
		defer func() { _ = reader.Close() }()
		raw, err := io.ReadAll(io.LimitReader(reader, 513))
		if err != nil || len(raw) > 512 {
			return errors.New("web push private key file exceeds its bounded format")
		}
		private = string(raw)
	}
	c.WebPushPrivateKey = strings.TrimSpace(private)
	if c.WebPushPrivateKey == "" {
		return nil
	}
	if len(c.WebPushPrivateKey) > 128 {
		return errors.New("invalid web push private key")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(c.WebPushPrivateKey)
	if err != nil || len(raw) != 32 {
		return errors.New("web push private key must be an unpadded base64url P-256 scalar")
	}
	if _, err = ecdh.P256().NewPrivateKey(raw); err != nil {
		return errors.New("web push private key is outside the P-256 scalar range")
	}
	return nil
}

func validWebPushContact(value string) bool {
	if len(value) > 512 {
		return false
	}
	for _, character := range value {
		if character < 33 || character > 126 {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Fragment != "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme == "mailto" {
		if parsed.RawQuery != "" || parsed.Host != "" || parsed.Path != "" || parsed.Opaque == "" {
			return false
		}
		address, err := mail.ParseAddress(parsed.Opaque)
		return err == nil && address.Name == "" && address.Address == parsed.Opaque
	}
	if parsed.Scheme != "https" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.ContainsAny(parsed.Path, "\r\n") || parsed.Port() != "" && parsed.Port() != "443" {
		return false
	}
	_, err = normalizeOrigin("https://"+parsed.Host, true)
	return err == nil
}
