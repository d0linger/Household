package store

import (
	"encoding/json"
	"strings"
	"testing"

	"household/internal/module"
	"household/internal/modules/pantry"
	"household/internal/value"
)

func TestPantryIntakeLotDataDatesAndProvenance(t *testing.T) {
	s := Store{Modules: []module.Descriptor{pantry.Module}}
	good := PantryBatchLine{ProductID: value.UUID(), LocationID: value.UUID(), Label: "  Explicit lot  ", ReceivedOn: "2026-10-25"}
	data, err := s.pantryIntakeLotData(good)
	if err != nil || data["notes"] != "Explicit lot" || data["after_open_days"] != nil || data["after_open_source"] != "" {
		t.Fatal("unknown opening provenance", data, err)
	}
	for _, name := range []string{"missing date", "invalid leap date", "empty label", "long label", "unattributed opening", "provenance without duration"} {
		t.Run(name, func(t *testing.T) {
			in := good
			days := int64(3)
			switch name {
			case "missing date":
				in.ReceivedOn = ""
			case "invalid leap date":
				in.BestBefore = "2026-02-29"
			case "empty label":
				in.Label = "  "
			case "long label":
				in.Label = strings.Repeat("a", 301)
			case "unattributed opening":
				in.AfterOpenDays = &days
			case "provenance without duration":
				in.AfterOpenSource = "manufacturer"
			}
			if _, err := s.pantryIntakeLotData(in); err == nil {
				t.Fatal("invalid final lot metadata accepted")
			}
		})
	}
	days := int64(0)
	good.AfterOpenDays, good.AfterOpenSource, good.AfterOpenNote, good.OpenedOn = &days, "manufacturer", "Label on package", "2026-10-25"
	if data, err = s.pantryIntakeLotData(good); err != nil || Int(data["after_open_days"]) != 0 || data["after_open_note"] != "Label on package" {
		t.Fatal("explicit zero-day provenance lost", data, err)
	}
}

func TestPantryDraftCanonicalIdentityAndFingerprint(t *testing.T) {
	line := value.UUID()
	in := PantryBatchDraftInput{Generation: 2, Version: 3, Kind: "inventory", LocationID: value.UUID(), Lines: []PantryBatchLine{{ID: strings.ToUpper(line)}}}
	normalized, err := normalizePantryBatchDraft(in)
	if err != nil || normalized.Lines[0].ID != line {
		t.Fatal(normalized, err)
	}
	raw, _ := json.Marshal(normalized.Lines)
	var rows any
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	record := Record{"generation": 2, "version": 3, "kind": "inventory", "location_id": in.LocationID, "lines": rows}
	restored, err := pantryBatchDraftInput(record)
	first, firstErr := pantryBatchFingerprint(normalized)
	second, secondErr := pantryBatchFingerprint(restored)
	if err != nil || firstErr != nil || secondErr != nil || first != second {
		t.Fatal("stable complete saved-draft fingerprint", first, second, err)
	}
	restored.Version++
	third, _ := pantryBatchFingerprint(restored)
	if third == first {
		t.Fatal("draft version omitted from review fingerprint")
	}
	in.Lines = append(in.Lines, PantryBatchLine{ID: line})
	if _, err = normalizePantryBatchDraft(in); err == nil {
		t.Fatal("case-equivalent duplicate line identity accepted")
	}
}

func TestNormalizePantryBatchDraft(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      PantryBatchDraftInput
		wantErr bool
	}{
		{"empty intake", PantryBatchDraftInput{Generation: 1, Kind: "intake"}, false},
		{"incomplete amount", PantryBatchDraftInput{Generation: 1, Kind: "intake", Lines: []PantryBatchLine{{ID: value.UUID(), Quantity: "1,"}}}, false},
		{"empty inventory", PantryBatchDraftInput{Generation: 1, Kind: "inventory"}, false},
		{"too many lines", PantryBatchDraftInput{Generation: 1, Kind: "intake", Lines: make([]PantryBatchLine, 101)}, true},
		{"invalid generation", PantryBatchDraftInput{Generation: 0, Kind: "intake"}, true},
		{"invalid kind", PantryBatchDraftInput{Generation: 1, Kind: "consume"}, true},
		{"invalid title utf8", PantryBatchDraftInput{Generation: 1, Kind: "intake", Title: string([]byte{255})}, true},
		{"title bytes", PantryBatchDraftInput{Generation: 1, Kind: "intake", Title: strings.Repeat("ä", 51)}, true},
		{"existing intake lot", PantryBatchDraftInput{Generation: 1, Kind: "intake", Lines: []PantryBatchLine{{ID: value.UUID(), LotID: value.UUID()}}}, true},
		{"inventory metadata", PantryBatchDraftInput{Generation: 1, Kind: "inventory", Lines: []PantryBatchLine{{ID: value.UUID(), Label: "Cannot relabel"}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := normalizePantryBatchDraft(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatal(err)
			}
			if err == nil && out.Lines == nil {
				t.Fatal("empty draft lines must encode as an array")
			}
		})
	}
	id, lot, loc := value.UUID(), value.UUID(), value.UUID()
	for _, lines := range [][]PantryBatchLine{
		{{ID: id}, {ID: id}},
		{{ID: id, LotID: lot}, {ID: value.UUID(), LotID: lot}},
		{{ID: id, LocationID: value.UUID()}},
		{{ID: id, Reason: string([]byte{0})}},
		{{ID: id, MovementCount: -1}},
	} {
		if _, err := normalizePantryBatchDraft(PantryBatchDraftInput{Generation: 1, Kind: "inventory", LocationID: loc, Lines: lines}); err == nil {
			t.Fatal("invalid identity/stand accepted", lines)
		}
	}
	days := int64(3)
	in := PantryBatchDraftInput{Generation: 1, Kind: "intake", Title: "  Saved later  ", Lines: []PantryBatchLine{{ID: id, Quantity: "1,", AfterOpenDays: &days}}}
	out, err := normalizePantryBatchDraft(in)
	if err != nil || out.Title != "Saved later" || out.Lines[0].Quantity != "1," {
		t.Fatal(out, err)
	}
	out.Lines[0].Quantity = "2"
	*out.Lines[0].AfterOpenDays = 5
	if in.Lines[0].Quantity != "1," || days != 3 {
		t.Fatal("draft normalization aliased caller data")
	}
}

func TestPantryCountAmount(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		integers        bool
		wantErr         bool
	}{
		{"zero", "0", "0.000000", false, false},
		{"comma", " 0,5 ", "0.500000", false, false},
		{"large aggregate", "1000000000000.000001", "1000000000000.000001", false, false},
		{"max aggregate", "999999999999999999999999.999999", "999999999999999999999999.999999", false, false},
		{"missing", "", "", false, true},
		{"negative", "-1", "", false, true},
		{"precision", "0.0000001", "", false, true},
		{"range", "1000000000000000000000000", "", false, true},
		{"integer", "1.5", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pantryCountAmount(tc.raw, tc.integers)
			if (err != nil) != tc.wantErr || !tc.wantErr && got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}

func TestPantryInventoryDelta(t *testing.T) {
	for _, tc := range []struct {
		name, before, count, want string
		integers, wantErr         bool
	}{
		{"same", "2", "2", "0.000000", false, false},
		{"empty", "2", "0", "-2.000000", false, false},
		{"more", "2", "3.000001", "1.000001", false, false},
		{"large close count", "1000000000000", "1000000000000.000001", "0.000001", false, false},
		{"unbookable difference", "1000000000000", "0", "", false, true},
		{"missing", "2", "", "", false, true},
		{"fractional integer count", "2", "1.5", "", true, true},
		{"negative book", "-1", "0", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pantryInventoryDelta(tc.before, tc.count, tc.integers)
			if (err != nil) != tc.wantErr || !tc.wantErr && got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}

func TestPantryIntakeAmount(t *testing.T) {
	for _, tc := range []struct {
		name, q, unit, base, want string
		pack                      Record
		integers, wantErr         bool
	}{
		{"grams", "250", "g", "kg", "0.250000", nil, false, false},
		{"exact pack", "2", "", "kg", "1.000002", Record{"quantity": "500.001", "unit_code": "g"}, false, false},
		{"partial pack", "0,5", "", "piece", "3.000000", Record{"quantity": "6", "unit_code": "piece"}, true, false},
		{"fractional integer pack", "0.5", "", "piece", "", Record{"quantity": "3", "unit_code": "piece"}, true, true},
		{"tiny", "0.000001", "g", "kg", "", nil, false, true},
		{"incompatible", "1", "l", "kg", "", nil, false, true},
		{"unknown pack", "1", "", "kg", "", Record{}, false, true},
		{"zero", "0", "g", "kg", "", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pantryIntakeAmount(tc.q, tc.unit, tc.base, tc.pack, tc.integers)
			if (err != nil) != tc.wantErr || !tc.wantErr && got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}
