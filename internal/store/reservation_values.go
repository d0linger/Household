package store

import (
	"encoding/hex"
	"math/big"
	"slices"
	"strings"
	"time"

	"household/internal/value"
)

const reservationAllocationLimit = 100

type ReservationAllocation struct {
	LotID           string `json:"lot_id"`
	ProductID       string `json:"product_id"`
	ProductVersion  int64  `json:"product_version"`
	LotVersion      int64  `json:"lot_version"`
	LocationVersion int64  `json:"location_version"`
	MovementCount   int64  `json:"movement_count"`
	Balance         string `json:"balance"`
	OtherReserved   string `json:"other_reserved"`
	Quantity        string `json:"quantity"`
	Unit            string `json:"unit"`
}

type ReservationInput struct {
	Generation       int64                   `json:"generation"`
	PantryGeneration int64                   `json:"pantry_generation"`
	SourceBasis      string                  `json:"source_basis"`
	PreviousID       string                  `json:"previous_id"`
	PreviousVersion  int64                   `json:"previous_version"`
	ExpiresAt        string                  `json:"expires_at"`
	Allocations      []ReservationAllocation `json:"allocations"`
}

func normalizeReservation(in ReservationInput) (ReservationInput, error) {
	basis, err := hex.DecodeString(in.SourceBasis)
	if err != nil || len(basis) != 32 || in.Generation < 1 || in.PantryGeneration < 1 || len(in.Allocations) < 1 || len(in.Allocations) > reservationAllocationLimit {
		return in, Invalid{"Geprüfte Modul- und Quellstände sowie 1 bis 100 Chargenzuweisungen erforderlich"}
	}
	if in.PreviousID == "" && in.PreviousVersion != 0 || in.PreviousID != "" && (!value.ValidUUID(in.PreviousID) || in.PreviousVersion < 1) {
		return in, Invalid{"Den zuletzt geprüften Reservierungsstand übermitteln"}
	}
	expiry, err := time.Parse(time.RFC3339Nano, in.ExpiresAt)
	if err != nil || expiry.Year() < 1900 || expiry.Year() > 9999 {
		return in, Invalid{"Geprüfte Ablaufzeit mit Zeitzone erforderlich"}
	}
	in.ExpiresAt, in.SourceBasis = expiry.UTC().Format(time.RFC3339Nano), hex.EncodeToString(basis)
	in.Allocations = slices.Clone(in.Allocations)
	seen := map[string]bool{}
	for i := range in.Allocations {
		a := &in.Allocations[i]
		q, quantityErr := value.Decimal(a.Quantity)
		balance, balanceErr := stockDecimal(a.Balance)
		other, otherErr := stockDecimal(a.OtherReserved)
		if !value.ValidUUID(a.LotID) || !value.ValidUUID(a.ProductID) || seen[a.LotID] || a.ProductVersion < 1 || a.LotVersion < 1 || a.LocationVersion < 1 || a.MovementCount < 0 ||
			quantityErr != nil || q.Sign() <= 0 || balanceErr != nil || balance.Sign() < 0 || otherErr != nil || other.Sign() < 0 ||
			(a.Unit != "piece" && a.Unit != "g" && a.Unit != "kg" && a.Unit != "ml" && a.Unit != "l") {
			return in, Invalid{"Eindeutige Chargen mit geprüften Versionen, exaktem Bestand, Fremdreservierung und positiver Basismenge erforderlich"}
		}
		a.Quantity, a.Balance, a.OtherReserved = q.FloatString(6), balance.FloatString(6), other.FloatString(6)
		seen[a.LotID] = true
	}
	slices.SortFunc(in.Allocations, func(a, b ReservationAllocation) int { return strings.Compare(a.LotID, b.LotID) })
	return in, nil
}

// ReservationCoverage keeps physical truth separate from soft planning claims.
// Own is the selected source set; its claims are already part of Reserved.
type ReservationCoverage struct {
	Physical     string `json:"physical"`
	Own          string `json:"own"`
	Other        string `json:"other"`
	Reserved     string `json:"reserved"`
	Free         string `json:"free"`
	UsableForOwn string `json:"usable_for_own"`
	OwnCovered   string `json:"own_covered"`
	Undercovered string `json:"undercovered"`
}

func reservationCoverage(physical, own, other string) (ReservationCoverage, error) {
	var result ReservationCoverage
	amounts := make([]*big.Rat, 0, 3)
	for _, raw := range []string{physical, own, other} {
		amount, err := stockDecimal(raw)
		if err != nil || amount.Sign() < 0 {
			return result, Invalid{"Bestand und Reservierungen als nichtnegative exakte Mengen übermitteln"}
		}
		amounts = append(amounts, amount)
	}
	stock, selected, others := amounts[0], amounts[1], amounts[2]
	reserved := new(big.Rat).Add(selected, others)
	free := reservationNonnegative(new(big.Rat).Sub(stock, reserved))
	usable := reservationNonnegative(new(big.Rat).Sub(stock, others))
	covered := new(big.Rat).Set(selected)
	if covered.Cmp(usable) > 0 {
		covered.Set(usable)
	}
	under := reservationNonnegative(new(big.Rat).Sub(reserved, stock))
	return ReservationCoverage{
		Physical: stock.FloatString(6), Own: selected.FloatString(6), Other: others.FloatString(6),
		Reserved: reserved.FloatString(6), Free: free.FloatString(6), UsableForOwn: usable.FloatString(6),
		OwnCovered: covered.FloatString(6), Undercovered: under.FloatString(6),
	}, nil
}

func reservationNonnegative(amount *big.Rat) *big.Rat {
	if amount.Sign() < 0 {
		amount.SetInt64(0)
	}
	return amount
}
