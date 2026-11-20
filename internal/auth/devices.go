package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"strings"

	"household/internal/securityevent"

	"github.com/jackc/pgx/v5"
)

// DeviceLabel reduces a user agent to "browser · system". It is coarse on
// purpose: a browser update is not a new device, and nothing identifying is kept.
func DeviceLabel(userAgent string) string {
	ua := strings.ToLower(userAgent)
	browser := "Other browser"
	switch {
	case strings.Contains(ua, "edg/") || strings.Contains(ua, "edga/") || strings.Contains(ua, "edgios/"):
		browser = "Edge"
	case strings.Contains(ua, "opr/") || strings.Contains(ua, "opera"):
		browser = "Opera"
	case strings.Contains(ua, "firefox/") || strings.Contains(ua, "fxios/"):
		browser = "Firefox"
	case strings.Contains(ua, "chrome/") || strings.Contains(ua, "crios/") || strings.Contains(ua, "chromium/"):
		browser = "Chrome"
	case strings.Contains(ua, "safari/"):
		browser = "Safari"
	}
	system := "Other system"
	switch {
	case strings.Contains(ua, "android"):
		system = "Android"
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ipod"):
		system = "iOS"
	case strings.Contains(ua, "windows"):
		system = "Windows"
	case strings.Contains(ua, "mac os x") || strings.Contains(ua, "macintosh"):
		system = "macOS"
	case strings.Contains(ua, "cros"):
		system = "ChromeOS"
	case strings.Contains(ua, "linux"):
		system = "Linux"
	}
	return browser + " · " + system
}

// deviceHash keys the label with the account and the rate secret, so stored
// hashes cannot be compared across accounts or installations.
func (s *Service) deviceHash(user, userAgent string) []byte {
	mac := hmac.New(sha256.New, []byte(s.cfg.RateSecret))
	mac.Write([]byte("household-known-device\x00" + user + "\x00" + DeviceLabel(userAgent)))
	return mac.Sum(nil)
}

// noteDevice remembers the browser/system of a new session and records a
// personal security event when it is new for an account whose device history
// is older than one day (so the first sign-ins after an upgrade only learn).
// It runs in a savepoint: a failure never blocks the sign-in itself.
func (s *Service) noteDevice(ctx context.Context, tx pgx.Tx, user, userAgent string) {
	nested, err := tx.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = nested.Rollback(ctx) }()
	var inserted, learned bool
	err = nested.QueryRow(ctx, `WITH history AS (SELECT count(*) AS known,min(first_seen) AS oldest FROM core.known_devices WHERE user_id=$1 AND device_hash<>$2),
 upsert AS (INSERT INTO core.known_devices(user_id,device_hash) VALUES($1,$2)
  ON CONFLICT(user_id,device_hash) DO UPDATE SET last_seen=now() RETURNING (xmax=0) AS inserted)
 SELECT upsert.inserted,history.known>0 AND history.oldest<now()-interval '1 day' FROM upsert,history`, user, s.deviceHash(user, userAgent)).Scan(&inserted, &learned)
	if err != nil {
		return
	}
	if _, err = nested.Exec(ctx, "DELETE FROM core.known_devices WHERE user_id=$1 AND last_seen<now()-interval '400 days'", user); err != nil {
		return
	}
	if inserted && learned {
		if err = securityevent.RecordAccount(ctx, nested, user, "sign_in_new_device"); err != nil {
			return
		}
	}
	_ = nested.Commit(ctx)
}
