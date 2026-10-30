package recipeimport

import (
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
)

func valueRatMillion() *big.Rat { return big.NewRat(1_000_000, 1) }

// jsonDepth counts structural nesting without treating brackets inside JSON
// strings as syntax. The normal decoder still verifies complete JSON grammar.
func jsonDepth(raw []byte) int {
	depth, maximum := 0, 0
	inString, escaped := false, false
	for _, b := range raw {
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return 33
			}
		}
		if maximum > 32 {
			return maximum
		}
	}
	if depth != 0 || inString {
		return 33
	}
	return maximum
}

func canonicalURL(raw string) (string, error) {
	if len(raw) > 2048 {
		return "", errors.New("URL ist zu lang")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.Host == "" || u.Port() != "" && u.Port() != "443" {
		return "", errors.New("nur öffentliche HTTPS-Quellen auf Port 443 sind zulässig")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if len(host) < 4 || len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", errors.New("quellname ist ungültig")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("quellname ist ungültig")
		}
		for _, char := range label {
			if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return "", errors.New("quellname ist ungültig")
			}
		}
	}
	u.Scheme, u.Host, u.User = "https", host, nil
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String(), nil
}
