// Package config reads and validates deployment configuration without logging secrets.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"household/internal/documentinput"
	"household/internal/meterfeed"
)

type Config struct {
	Listen             string
	Origin             string
	DatabaseURL        string
	CSRFSecret         string
	EncryptionKey      string
	RateSecret         string
	AdminUsername      string
	AdminPassword      string
	HouseholdName      string
	UploadDir          string
	Production         bool
	TrustedProxies     []*net.IPNet
	MaxConnections     int32
	SessionIdle        time.Duration
	SessionAbsolute    time.Duration
	UploadQuota        int64
	OCRCommand         string
	OCREnabled         bool
	FoodFactsEnabled   bool
	FoodFactsUserAgent string
	FoodFactsStaging   bool
	WebPushEnabled     bool
	WebPushPrivateKey  string
	WebPushContact     string
	MetricsToken       string
	MeterFeeds         meterfeed.Config
	DocumentInputs     documentinput.Config
}

var hostnameLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var numericHostLabel = regexp.MustCompile(`^(?:[0-9]+|0x[0-9a-f]*)$`)

func secret(name string) (string, error) {
	v, path := os.Getenv(name), os.Getenv(name+"_FILE")
	if v != "" && path != "" {
		return "", fmt.Errorf("both %s and file are configured", name)
	}
	if path != "" {
		// security: secret-file paths come solely from deployment configuration.
		b, err := os.ReadFile(path) // #nosec G304 G703 -- trusted operator secret path
		if err != nil {
			return "", fmt.Errorf("read %s secret file: %w", name, err)
		}
		v = strings.TrimSpace(string(b))
	}
	return v, nil
}

func Load() (Config, error) {
	mode := env("HOUSEHOLD_ENV", "production")
	if mode != "production" && mode != "development" {
		return Config{}, errors.New("HOUSEHOLD_ENV must be production or development")
	}
	c := Config{Listen: env("HOUSEHOLD_LISTEN", ":8080"), Origin: env("HOUSEHOLD_ORIGIN", "http://localhost:8100"), AdminUsername: env("HOUSEHOLD_ADMIN_USERNAME", "admin"), HouseholdName: env("HOUSEHOLD_NAME", "Mein Haushalt"), UploadDir: env("HOUSEHOLD_UPLOAD_DIR", ".local/uploads"), Production: env("HOUSEHOLD_ENV", "production") == "production", MaxConnections: 10, SessionIdle: 24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour, UploadQuota: 2 << 30, OCRCommand: os.Getenv("HOUSEHOLD_OCR_COMMAND")}
	var err error
	if raw := os.Getenv("HOUSEHOLD_OCR_ENABLED"); raw != "" {
		c.OCREnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("invalid OCR setting")
		}
	}
	if raw := os.Getenv("HOUSEHOLD_OFF_ENABLED"); raw != "" {
		c.FoodFactsEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("invalid Open Food Facts setting")
		}
	}
	if raw := os.Getenv("HOUSEHOLD_OFF_STAGING"); raw != "" {
		c.FoodFactsStaging, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("invalid Open Food Facts staging setting")
		}
	}
	if c.FoodFactsStaging && c.Production {
		return Config{}, errors.New("open food facts staging requires development mode")
	}
	c.FoodFactsUserAgent = strings.TrimSpace(os.Getenv("HOUSEHOLD_OFF_USER_AGENT"))
	if len(c.FoodFactsUserAgent) > 256 {
		return Config{}, errors.New("open food facts User-Agent exceeds 256 bytes")
	}
	for _, character := range c.FoodFactsUserAgent {
		if character < 32 || character > 126 {
			return Config{}, errors.New("open food facts User-Agent requires printable ASCII")
		}
	}
	if err = loadWebPush(&c); err != nil {
		return Config{}, err
	}
	if err = loadMeterFeeds(&c); err != nil {
		return Config{}, err
	}
	if err = loadDocumentInputs(&c); err != nil {
		return Config{}, err
	}
	for name, target := range map[string]*string{"HOUSEHOLD_DATABASE_URL": &c.DatabaseURL, "HOUSEHOLD_CSRF_SECRET": &c.CSRFSecret, "HOUSEHOLD_ENCRYPTION_KEY": &c.EncryptionKey, "HOUSEHOLD_RATE_SECRET": &c.RateSecret, "HOUSEHOLD_ADMIN_PASSWORD": &c.AdminPassword, "HOUSEHOLD_METRICS_TOKEN": &c.MetricsToken} {
		*target, err = secret(name)
		if err != nil {
			return Config{}, err
		}
	}
	c.Origin, err = normalizeOrigin(c.Origin, c.Production)
	if err != nil {
		return Config{}, err
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("database URL is required")
	}
	for name, key := range map[string]string{"CSRF": c.CSRFSecret, "encryption": c.EncryptionKey, "rate": c.RateSecret} {
		if len(strings.TrimSpace(key)) < 32 || strings.Contains(strings.ToLower(key), "change-me") {
			return Config{}, fmt.Errorf("%s secret must contain at least 32 non-placeholder characters", name)
		}
	}
	if c.CSRFSecret == c.EncryptionKey || c.CSRFSecret == c.RateSecret || c.EncryptionKey == c.RateSecret {
		return Config{}, errors.New("security secrets must be independent")
	}
	if raw := os.Getenv("HOUSEHOLD_DB_MAX_CONNECTIONS"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 2 || n > 100 {
			return Config{}, errors.New("invalid database connection limit")
		}
		c.MaxConnections = int32(n) // #nosec G109 -- explicitly bounded to 2..100 above
	}
	for _, raw := range strings.Split(os.Getenv("HOUSEHOLD_TRUSTED_PROXIES"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		_, network, e := net.ParseCIDR(strings.TrimSpace(raw))
		if e != nil {
			return Config{}, errors.New("invalid trusted proxy network")
		}
		c.TrustedProxies = append(c.TrustedProxies, network)
	}
	return c, nil
}

func normalizeOrigin(raw string, production bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid household origin")
	}
	invalidPath := u.Path != "" && u.Path != "/"
	hasURLSuffix := u.ForceQuery || u.RawQuery != "" || strings.Contains(raw, "#")
	if u.Hostname() == "" || u.User != nil || invalidPath || hasURLSuffix {
		return "", errors.New("invalid household origin")
	}
	if production && u.Scheme != "https" {
		return "", errors.New("production origin requires https")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("unsupported origin scheme")
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsAny(u.Host, "[]") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return "", errors.New("invalid household origin host")
		}
		host = "[" + address.String() + "]"
		if address.Is4In6() {
			octets := address.As16()
			high := uint16(octets[12])<<8 | uint16(octets[13])
			low := uint16(octets[14])<<8 | uint16(octets[15])
			host = fmt.Sprintf("[::ffff:%x:%x]", high, low)
		}
	} else if address, err := netip.ParseAddr(host); err != nil || !address.Is4() {
		dnsName := strings.TrimSuffix(host, ".")
		labels := strings.Split(dnsName, ".")
		if len(dnsName) > 253 || numericHostLabel.MatchString(labels[len(labels)-1]) {
			return "", errors.New("invalid household origin host")
		}
		for _, label := range labels {
			if !hostnameLabel.MatchString(label) {
				return "", errors.New("origin host must be a DNS name in ASCII or an IP address")
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid household origin port")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid household origin port")
		}
		isDefaultPort := (u.Scheme == "http" && n == 80) || (u.Scheme == "https" && n == 443)
		if !isDefaultPort {
			host += ":" + strconv.Itoa(n)
		}
	}
	// Browser Origin headers omit a root slash and default ports. Keep the
	// configured origin in that same form for the exact CSRF origin comparison.
	return u.Scheme + "://" + host, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
