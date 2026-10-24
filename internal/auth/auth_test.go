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
