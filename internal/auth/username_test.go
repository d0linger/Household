package auth

import (
	"strings"
	"testing"
)

func TestUsernameValid(t *testing.T) {
	for _, name := range []string{"daniel.preining", "traveler_01", "Daniel+home@example.org", strings.Repeat("a", 64) + "@example.org"} {
		if !UsernameValid(name) {
			t.Errorf("valid username rejected: %q", name)
		}
	}
	for _, name := range []string{"x", "a..b@example.org", "a@@example.org", "a@example.-org", "a@example", "Dana <a@example.org>", strings.Repeat("a", 65) + "@example.org"} {
		if UsernameValid(name) {
			t.Errorf("invalid username accepted: %q", name)
		}
	}
}
