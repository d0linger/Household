package auth

import (
	"household/internal/config"
	"strings"
	"testing"
)

func TestCSRFBinding(t *testing.T) {
	t.Parallel()
	s := &Service{cfg: config.Config{CSRFSecret: strings.Repeat("x", 32)}}
	raw := RandomToken()
	token := s.CSRF(raw, "session")
	if !s.ValidCSRF(raw, "session", token) {
		t.Fatal("own token rejected")
	}
	for _, tc := range []struct{ name, raw, kind, token string }{{"different session", RandomToken(), "session", token}, {"login token", raw, "login", token}, {"tampered", raw, "session", token[:40] + "aaa"}, {"empty", raw, "session", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if s.ValidCSRF(tc.raw, tc.kind, tc.token) {
				t.Fatal("unbound token accepted")
			}
		})
	}
}
func TestPasswordValid(t *testing.T) {
	t.Parallel()
	if PasswordValid("short") || PasswordValid(strings.Repeat("x", 73)) || !PasswordValid("a very long passphrase") {
		t.Fatal("password bounds failed")
	}
	if PasswordValid(strings.Repeat("界", 25)) {
		t.Fatal("bcrypt byte bound ignored")
	}
}
func TestNetworkPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, ip, want string }{
		{"ipv4", "203.0.113.77", "203.0.113.0/24"},
		{"ipv4 mapped", "::ffff:203.0.113.77", "203.0.113.0/24"},
		{"ipv6", "2001:db8:aa:bbcc::1", "2001:db8:aa:bb00::/56"},
		{"ipv6 zone", "fe80::1%eth0", "fe80::/56"},
		{"unparsable", "unknown", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := networkPrefix(tc.ip); got != tc.want {
				t.Fatalf("networkPrefix(%q) = %q, want %q", tc.ip, got, tc.want)
			}
		})
	}
	if networkPrefix("203.0.113.1") != networkPrefix("203.0.113.254") || networkPrefix("203.0.113.1") == networkPrefix("203.0.114.1") {
		t.Fatal("neighbouring addresses must share a budget and other networks must not")
	}
}
