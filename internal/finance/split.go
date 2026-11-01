package finance

import (
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"household/internal/value"
)

var ErrSplit = errors.New("vollständige exakte Aufteilung mit eindeutigen Personen prüfen")

type Participant struct {
	MembershipID string `json:"membership_id"`
	Basis        string `json:"basis"`
}

type SplitInput struct {
	Method       string        `json:"method"`
	Participants []Participant `json:"participants"`
}

type Share struct {
	MembershipID string `json:"membership_id"`
	Minor        string `json:"minor"`
}

type LineAmount struct {
	ID    string
	Minor int64
}

type quota struct {
	id        string
	value     *big.Rat
	remainder *big.Rat
	whole     int64
	index     int
}

// Split retains the input presentation order and resolves rounding ties by UUID.
func Split(input SplitInput, total int64) ([]Share, error) {
	if total == 0 || total < -MinorLimit || total > MinorLimit || len(input.Participants) == 0 || len(input.Participants) > 50 {
		return nil, ErrSplit
	}
	seen := make(map[string]bool, len(input.Participants))
	weights := make([]int64, len(input.Participants))
	var sum int64
	for i, p := range input.Participants {
		if !value.ValidUUID(p.MembershipID) || strings.ToLower(p.MembershipID) != p.MembershipID || seen[p.MembershipID] {
			return nil, ErrSplit
		}
		seen[p.MembershipID] = true
		weight, err := ParseMinor(p.Basis)
		if err != nil {
			return nil, ErrSplit
		}
		switch input.Method {
		case "equal":
			if weight != 1 {
				return nil, ErrSplit
			}
		case "percent":
			if weight < 0 || weight > 1_000_000 {
				return nil, ErrSplit
			}
		case "weights":
			if weight <= 0 || weight > 1_000_000_000 {
				return nil, ErrSplit
			}
		case "fixed":
			if total > 0 && weight < 0 || total < 0 && weight > 0 {
				return nil, ErrSplit
			}
		default:
			return nil, ErrSplit
		}
		weights[i] = weight
		sum += weight
	}
	if input.Method == "fixed" {
		if sum != total {
			return nil, ErrSplit
		}
		out := make([]Share, len(weights))
		for i, amount := range weights {
			out[i] = Share{MembershipID: input.Participants[i].MembershipID, Minor: strconv.FormatInt(amount, 10)}
		}
		return out, nil
	}
	if sum <= 0 || input.Method == "percent" && sum != 1_000_000 {
		return nil, ErrSplit
	}
	abs := total
	if abs < 0 {
		abs = -abs
	}
	quotas := make([]quota, len(weights))
	for i, weight := range weights {
		n := new(big.Int).Mul(big.NewInt(abs), big.NewInt(weight))
		quotas[i] = quota{id: input.Participants[i].MembershipID, value: new(big.Rat).SetFrac(n, big.NewInt(sum)), index: i}
	}
	amounts, err := allocate(quotas, abs)
	if err != nil {
		return nil, err
	}
	out := make([]Share, len(weights))
	for i, amount := range amounts {
		if total < 0 {
			amount = -amount
		}
		out[i] = Share{MembershipID: input.Participants[i].MembershipID, Minor: strconv.FormatInt(amount, 10)}
	}
	return out, nil
}

// ConvertLines converts a draft's line sum independently from its header. Only
// a fully allocated original header can be posted, so no incomplete sum is hidden.
func ConvertLines(lines []LineAmount, currency string, exponent int, fx *FXInput) ([]int64, error) {
	if len(lines) > 100 {
		return nil, ErrMoney
	}
	knownExponent, ok := CurrencyExponent(currency)
	if !ok || knownExponent != exponent {
		return nil, ErrMoney
	}
	rate := big.NewRat(1, 1)
	if currency != "EUR" {
		var err error
		rate, err = Rate(fx)
		if err != nil {
			return nil, err
		}
	} else if fx != nil {
		return nil, ErrMoney
	}
	quotas := make([]quota, len(lines))
	seen := make(map[string]bool, len(lines))
	var originalTotal int64
	for i, line := range lines {
		if !value.ValidUUID(line.ID) || strings.ToLower(line.ID) != line.ID || seen[line.ID] || line.Minor == 0 || line.Minor < -MinorLimit || line.Minor > MinorLimit {
			return nil, ErrMoney
		}
		seen[line.ID] = true
		originalTotal += line.Minor
		quotas[i] = quota{id: line.ID, value: convertQuota(line.Minor, exponent, rate), index: i}
	}
	target, err := roundAggregate(convertQuota(originalTotal, exponent, rate))
	if err != nil {
		return nil, err
	}
	return allocate(quotas, target)
}

func roundAggregate(q *big.Rat) (int64, error) {
	n := new(big.Int).Abs(q.Num())
	whole, rem := new(big.Int), new(big.Int)
	whole.QuoRem(n, q.Denom(), rem)
	if new(big.Int).Lsh(rem, 1).Cmp(q.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if q.Sign() < 0 {
		whole.Neg(whole)
	}
	if !whole.IsInt64() {
		return 0, ErrMoney
	}
	return whole.Int64(), nil
}

func allocate(quotas []quota, target int64) ([]int64, error) {
	var sum int64
	for i := range quotas {
		q := &quotas[i]
		whole, rem := new(big.Int), new(big.Int)
		whole.DivMod(q.value.Num(), q.value.Denom(), rem)
		if !whole.IsInt64() || whole.Int64() < -MinorLimit-1 || whole.Int64() > MinorLimit {
			return nil, ErrMoney
		}
		q.whole = whole.Int64()
		q.remainder = new(big.Rat).SetFrac(rem, q.value.Denom())
		sum += q.whole
	}
	left := target - sum
	if left < 0 || left > int64(len(quotas)) {
		return nil, ErrMoney
	}
	slices.SortFunc(quotas, func(a, b quota) int {
		if cmp := b.remainder.Cmp(a.remainder); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.id, b.id)
	})
	for i := int64(0); i < left; i++ {
		quotas[i].whole++
	}
	out := make([]int64, len(quotas))
	for _, q := range quotas {
		if q.whole < -MinorLimit || q.whole > MinorLimit {
			return nil, ErrMoney
		}
		out[q.index] = q.whole
	}
	return out, nil
}
