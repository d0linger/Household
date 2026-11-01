// Package finance implements exact bounded financial values without persistence.
package finance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/value"
)

const MinorLimit int64 = 1_000_000_000_000

var ErrMoney = errors.New("betrag, Währung und geprüften exakten Kurs prüfen")

type FXInput struct {
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
	EffectiveOn string `json:"effective_on"`
	Source      string `json:"source"`
}

type MoneyInput struct {
	Currency string   `json:"currency"`
	Exponent int      `json:"exponent"`
	Minor    string   `json:"minor"`
	FX       *FXInput `json:"fx"`
}

type Money struct {
	Currency  string
	Exponent  int
	Minor     int64
	BaseMinor *int64
	FX        *FXInput
}

type Currency struct {
	Code     string `json:"currency"`
	Exponent int    `json:"exponent"`
}

func Currencies() []Currency {
	codes := []string{"EUR", "USD", "GBP", "CHF", "CAD", "AUD", "NZD", "SEK", "NOK", "DKK", "PLN", "CZK", "HUF", "RON", "JPY", "KWD", "BHD"}
	out := make([]Currency, 0, len(codes))
	for _, code := range codes {
		exponent, _ := CurrencyExponent(code)
		out = append(out, Currency{Code: code, Exponent: exponent})
	}
	return out
}

// CurrencyExponent is a closed local denomination catalogue, not an FX feed.
func CurrencyExponent(currency string) (int, bool) {
	switch currency {
	case "EUR", "USD", "GBP", "CHF", "CAD", "AUD", "NZD", "SEK", "NOK", "DKK", "PLN", "CZK", "HUF", "RON":
		return 2, true
	case "JPY":
		return 0, true
	case "KWD", "BHD":
		return 3, true
	default:
		return 0, false
	}
}

func ParseMinor(raw string) (int64, error) {
	if raw == "" || len(raw) > 14 || strings.TrimSpace(raw) != raw || raw == "-0" {
		return 0, ErrMoney
	}
	digits := raw
	if digits[0] == '-' {
		digits = digits[1:]
	}
	if digits == "" || len(digits) > 1 && digits[0] == '0' {
		return 0, ErrMoney
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return 0, ErrMoney
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < -MinorLimit || n > MinorLimit {
		return 0, ErrMoney
	}
	return n, nil
}

func NormalizeMoney(input MoneyInput, allowZero, allowUnknown bool) (Money, error) {
	exponent, supported := CurrencyExponent(input.Currency)
	minor, err := ParseMinor(input.Minor)
	if err != nil || !supported || exponent != input.Exponent || !allowZero && minor == 0 {
		return Money{}, ErrMoney
	}
	out := Money{Currency: input.Currency, Exponent: exponent, Minor: minor}
	if input.Currency == "EUR" {
		if input.FX != nil {
			return Money{}, ErrMoney
		}
		out.BaseMinor = new(int64)
		*out.BaseMinor = minor
		return out, nil
	}
	if input.FX == nil {
		if allowZero && minor == 0 {
			out.BaseMinor = new(int64)
			return out, nil
		}
		if !allowUnknown {
			return Money{}, ErrMoney
		}
		return out, nil
	}
	rate, err := Rate(input.FX)
	if err != nil {
		return Money{}, err
	}
	base, err := roundedInt(convertQuota(minor, exponent, rate))
	if err != nil {
		return Money{}, err
	}
	out.BaseMinor = &base
	copyRate := *input.FX
	out.FX = &copyRate
	return out, nil
}

// Rate validates EUR major units per one original major unit.
func Rate(input *FXInput) (*big.Rat, error) {
	if input == nil || !value.Date(input.EffectiveOn) || input.Source == "" ||
		strings.TrimSpace(input.Source) != input.Source || len(input.Source) > 800 ||
		!utf8.ValidString(input.Source) || utf8.RuneCountInString(input.Source) > 200 || strings.ContainsRune(input.Source, '\x00') {
		return nil, ErrMoney
	}
	n, okN := positiveInteger(input.Numerator)
	d, okD := positiveInteger(input.Denominator)
	if !okN || !okD {
		return nil, ErrMoney
	}
	return new(big.Rat).SetFrac(n, d), nil
}

func positiveInteger(raw string) (*big.Int, bool) {
	if raw == "" || len(raw) > 18 || raw[0] == '0' {
		return nil, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(raw, 10)
	return n, ok && n.Sign() > 0
}

func convertQuota(minor int64, exponent int, rate *big.Rat) *big.Rat {
	scale := int64(1)
	for range exponent {
		scale *= 10
	}
	return new(big.Rat).Mul(new(big.Rat).SetFrac(big.NewInt(minor*100), big.NewInt(scale)), rate)
}

func roundedInt(q *big.Rat) (int64, error) {
	n := new(big.Int).Abs(q.Num())
	d := q.Denom()
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(n, d, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(d) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if q.Sign() < 0 {
		whole.Neg(whole)
	}
	if !whole.IsInt64() || whole.Int64() < -MinorLimit || whole.Int64() > MinorLimit {
		return 0, ErrMoney
	}
	return whole.Int64(), nil
}

// StableID makes child identities independent of preview rollback or retries.
func StableID(parent, purpose string) string {
	sum := sha256.Sum256([]byte(parent + "\x00" + purpose))
	sum[6] = sum[6]&0x0f | 0x80
	sum[8] = sum[8]&0x3f | 0x80
	raw := hex.EncodeToString(sum[:16])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

func CivilDate(raw string) bool {
	parsed, err := time.Parse(time.DateOnly, raw)
	return err == nil && parsed.Year() >= 1900 && parsed.Year() <= 2200
}
