// Package value provides exact, bounded values at the application boundary.
package value

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var decimalPattern = regexp.MustCompile(`^-?[0-9]{1,12}([.,][0-9]{1,6})?$`)

func UUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cryptographic random source unavailable")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func ValidUUID(s string) bool { return uuidPattern.MatchString(s) }

func Decimal(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	if !decimalPattern.MatchString(s) {
		return nil, errors.New("invalid decimal")
	}
	r, ok := new(big.Rat).SetString(strings.ReplaceAll(s, ",", "."))
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	return r, nil
}

func Minor(s string) (int64, error) {
	r, err := Decimal(s)
	if err != nil {
		return 0, err
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, errors.New("money requires at most two decimals")
	}
	n := r.Num().Int64()
	if n > 1000000000000 || n < -1000000000000 {
		return 0, errors.New("money exceeds limit")
	}
	return n, nil
}

func Date(s string) bool {
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Year() >= 1900 && t.Year() <= 2200
}
