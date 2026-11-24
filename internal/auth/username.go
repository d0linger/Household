package auth

import (
	"regexp"
	"strings"
)

const MaxUsernameLength = 254

var (
	legacyUsername = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
	emailUsername  = regexp.MustCompile(`^[A-Za-z0-9_%+-]+([.][A-Za-z0-9_%+-]+)*@[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?([.][A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)
)

// UsernameValid accepts existing handles and unquoted ASCII email addresses.
func UsernameValid(username string) bool {
	if len(username) > MaxUsernameLength {
		return false
	}
	local, _, found := strings.Cut(username, "@")
	if !found {
		return legacyUsername.MatchString(username)
	}
	return len(local) <= 64 && emailUsername.MatchString(username)
}
