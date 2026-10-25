package store

import (
	"strings"
	"testing"
)

func TestReservationCoverage(t *testing.T) {
	tests := []struct {
		name, physical, own, other, reserved, free, usable, covered, under string
	}{
		{"empty", "0", "0", "0", "0", "0", "0", "0", "0"},
		{"unclaimed", "3", "0", "0", "0", "3", "3", "0", "0"},
		{"own counted once", "3", "1", "0", "1", "2", "3", "1", "0"},
		{"other source", "3", "0", "1", "1", "2", "2", "0", "0"},
		{"two sources", "3", "1", "1.5", "2.5", "0.5", "1.5", "1", "0"},
		{"fully claimed", "3", "1", "2", "3", "0", "1", "1", "0"},
		{"own undercovered", "1", "2", "0.5", "2.5", "0", "0.5", "0.5", "1.5"},
		{"other exceeds physical", "1", "2", "3", "5", "0", "0", "0", "4"},
		{"zero physical with claims", "0", "1", "2", "3", "0", "0", "0", "3"},
		{"six decimal amounts", "0.000003", "0.000001", "0.000001", "0.000002", "0.000001", "0.000002", "0.000001", "0"},
		{"wide physical", "999999999999999999999999.000003", "0.000001", "0.000001", "0.000002", "999999999999999999999999.000001", "999999999999999999999999.000002", "0.000001", "0"},
		{"wide reservation sum", "999999999999999999999999", "999999999999999999999999", "0.000001", "999999999999999999999999.000001", "0", "999999999999999999999998.999999", "999999999999999999999998.999999", "0.000001"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reservationCoverage(tc.physical, tc.own, tc.other)
			if err != nil {
				t.Fatal(err)
			}
			fields := []struct{ actual, expected string }{
				{got.Physical, tc.physical}, {got.Own, tc.own}, {got.Other, tc.other}, {got.Reserved, tc.reserved},
				{got.Free, tc.free}, {got.UsableForOwn, tc.usable}, {got.OwnCovered, tc.covered}, {got.Undercovered, tc.under},
			}
			for _, field := range fields {
				want, parseErr := stockDecimal(field.expected)
				if parseErr != nil || field.actual != want.FloatString(6) {
					t.Fatalf("coverage mismatch: %+v; got %s want %s", got, field.actual, field.expected)
				}
			}
		})
	}
}

func TestReservationCoverageRejectsInvalidAmounts(t *testing.T) {
	for _, raw := range []string{"", "NaN", "Infinity", "-0.000001", "1.0000001", "1/3", "1e3", "1000000000000000000000000000000"} {
		for index := range 3 {
			in := []string{"1", "0", "0"}
			in[index] = raw
			if got, err := reservationCoverage(in[0], in[1], in[2]); err == nil {
				t.Fatalf("invalid amount %q at %d accepted: %+v", raw, index, got)
			}
		}
	}
}

func reservationInputFixture() ReservationInput {
	return ReservationInput{Generation: 1, PantryGeneration: 1, SourceBasis: strings.Repeat("AB", 32), ExpiresAt: "2026-10-25T14:00:00+02:00",
		Allocations: []ReservationAllocation{{LotID: "10000000-0000-4000-8000-000000000002", ProductID: "10000000-0000-4000-8000-000000000001",
			ProductVersion: 1, LotVersion: 1, LocationVersion: 1, Quantity: "1", Balance: "2", OtherReserved: "0", Unit: "kg"}}}
}

func TestReservationNormalizationPreservesCaller(t *testing.T) {
	in := reservationInputFixture()
	second := in.Allocations[0]
	second.LotID = "10000000-0000-4000-8000-000000000001"
	in.Allocations = append(in.Allocations, second)
	got, err := normalizeReservation(in)
	if err != nil || got.SourceBasis != strings.Repeat("ab", 32) || got.ExpiresAt != "2026-10-25T12:00:00Z" || got.Allocations[0].LotID != second.LotID || got.Allocations[0].Quantity != "1.000000" {
		t.Fatal("equivalent reviewed input was not canonicalized", got, err)
	}
	if in.Allocations[0].LotID == second.LotID || in.Allocations[0].Quantity != "1" || in.SourceBasis != strings.Repeat("AB", 32) {
		t.Fatal("normalization mutated caller input", in)
	}
}

func TestReservationNormalizationBoundaries(t *testing.T) {
	for name, change := range map[string]func(*ReservationInput){
		"missing generation": func(in *ReservationInput) { in.Generation = 0 },
		"bad basis":          func(in *ReservationInput) { in.SourceBasis = strings.Repeat("z", 64) },
		"missing timezone":   func(in *ReservationInput) { in.ExpiresAt = "2026-10-25T12:00:00" },
		"unknown expiry":     func(in *ReservationInput) { in.ExpiresAt = "" },
		"missing prior id":   func(in *ReservationInput) { in.PreviousVersion = 1 },
		"missing prior version": func(in *ReservationInput) {
			in.PreviousID = "10000000-0000-4000-8000-000000000001"
		},
		"no allocations": func(in *ReservationInput) { in.Allocations = nil },
		"too many allocations": func(in *ReservationInput) {
			in.Allocations = make([]ReservationAllocation, reservationAllocationLimit+1)
		},
		"duplicate lot": func(in *ReservationInput) { in.Allocations = append(in.Allocations, in.Allocations[0]) },
		"zero quantity": func(in *ReservationInput) { in.Allocations[0].Quantity = "0" },
		"rounded quantity": func(in *ReservationInput) {
			in.Allocations[0].Quantity = "0.0000001"
		},
		"negative physical": func(in *ReservationInput) { in.Allocations[0].Balance = "-1" },
		"negative claims":   func(in *ReservationInput) { in.Allocations[0].OtherReserved = "-1" },
		"unknown unit":      func(in *ReservationInput) { in.Allocations[0].Unit = "packet" },
		"missing version":   func(in *ReservationInput) { in.Allocations[0].LotVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			in := reservationInputFixture()
			change(&in)
			if _, err := normalizeReservation(in); err == nil {
				t.Fatal("invalid reservation review accepted")
			}
		})
	}
}
