package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFuelModuleCaptureReceiptRetainsExactSourceEvidence(t *testing.T) {
	text := "Tankbeleg\nDatum 1.10.2026\nDiesel 40,25 l\nGesamt EUR 68,00\nKM-Stand: 123456,75\n"
	got, warnings, err := fuelModuleCaptureSuggestions("receipt", text, strings.ToUpper(fuelUnitAttachmentID), Record{})
	if err != nil {
		t.Fatal(err)
	}
	values := got["values"].(Record)
	for field, want := range map[string]string{"occurred_on": "2026-10-01", "quantity": "40.25", "quantity_unit": "l", "total_minor": "6800", "odometer": "123456.75"} {
		if values[field] != want {
			t.Fatalf("%s = %#v; want %s", field, values[field], want)
		}
	}
	hash := sha256.Sum256([]byte(text))
	if got["source"] != "local_ocr" || got["requires_confirmation"] != true || got["attachment_id"] != fuelUnitAttachmentID || got["text_sha256"] != hex.EncodeToString(hash[:]) || !reflect.DeepEqual(fuelUnitWarningCodes(warnings), []string{"ocr_review"}) {
		t.Fatalf("provenance or review requirement missing: %+v, %+v", got, warnings)
	}
	candidates := got["candidates"].(map[string][]fuelCaptureCandidate)
	quantity := candidates["quantity"]
	if len(quantity) != 1 || quantity[0].Line != 3 || quantity[0].Evidence != "Diesel 40,25 l" || quantity[0].Rule != "explicit_quantity_unit" || quantity[0].Attachment != fuelUnitAttachmentID {
		t.Fatalf("field evidence is not traceable to original line: %+v", quantity)
	}
	// Detection must not invent facts that only a person/current source can know.
	for _, absent := range []string{"currency_code", "full_tank", "previous_fill_missing", "odometer_series_id"} {
		if _, ok := values[absent]; ok {
			t.Fatalf("recognition invented reviewed field %s", absent)
		}
	}
	again, againWarnings, err := fuelModuleCaptureSuggestions("receipt", text, fuelUnitAttachmentID, Record{})
	if err != nil || !reflect.DeepEqual(got, again) || !reflect.DeepEqual(warnings, againWarnings) {
		t.Fatal("same local source produced unstable review", err)
	}
}

func TestFuelModuleCaptureAmbiguityNeverChoosesLastCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, text, field string
		count             int
	}{
		{"two totals", "Gesamt 68,00 und 70,00", "total_minor", 2},
		{"two dates", "2026-10-01\n2026-10-02", "occurred_on", 2},
		{"two quantities", "40 l\n42 l", "quantity", 2},
		{"mixed units", "40 l\n40 kg", "quantity_unit", 2},
		{"two readings", "KM-Stand 1000\nKM-Stand 2000", "odometer", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := fuelModuleCaptureSuggestions("receipt", tc.text, fuelUnitAttachmentID, Record{})
			if err != nil {
				t.Fatal(err)
			}
			values := got["values"].(Record)
			candidates := got["candidates"].(map[string][]fuelCaptureCandidate)
			if values[tc.field] != nil || len(candidates[tc.field]) != tc.count || !slices.Contains(fuelUnitWarningCodes(warnings), "ocr_ambiguous_"+tc.field) {
				t.Fatalf("ambiguous %s was selected or lost: %+v, %+v", tc.field, got, warnings)
			}
		})
	}
	got, warnings, err := fuelModuleCaptureSuggestions("receipt", "Gesamt 68,00\nGesamt 68.00", fuelUnitAttachmentID, Record{})
	if err != nil || got["values"].(Record)["total_minor"] != "6800" || len(got["candidates"].(map[string][]fuelCaptureCandidate)["total_minor"]) != 1 || slices.Contains(fuelUnitWarningCodes(warnings), "ocr_ambiguous_total_minor") {
		t.Fatalf("equivalent repeated evidence became ambiguity: %+v, %+v, %v", got, warnings, err)
	}
}

func TestFuelModuleCaptureRejectsMisleadingOrTruncatedNumbers(t *testing.T) {
	for _, tc := range []struct{ name, kind, text, field string }{
		{"unit price is not volume", "receipt", "Einzelpreis 1,79 l", "quantity"},
		{"English unit price", "receipt", "Unit price 1.79 l", "quantity"},
		{"VAT is not paid total", "receipt", "Gesamt MwSt 10,20", "total_minor"},
		{"third money place", "receipt", "Gesamt 68,001", "total_minor"},
		{"wide paid amount", "receipt", "Gesamt 9999999999999,00", "total_minor"},
		{"wide quantity", "receipt", "1234567890123,45 l", "quantity"},
		{"excess quantity precision", "receipt", "1,1234567 l", "quantity"},
		{"wide odometer", "odometer", "KM-Stand 1234567890123,45", "odometer"},
		{"labelled trip number", "odometer", "Trip A 123.4", "odometer"},
		{"joined display data", "odometer", "12345 678", "odometer"},
		{"invalid date", "receipt", "2026-02-29", "occurred_on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := fuelModuleCaptureSuggestions(tc.kind, tc.text, fuelUnitAttachmentID, Record{})
			if err != nil {
				t.Fatal(err)
			}
			if value := got["values"].(Record)[tc.field]; value != nil {
				t.Fatalf("misleading token %q became exact %s=%v", tc.text, tc.field, value)
			}
		})
	}
}

func TestFuelModuleCapturePhotoRequiresExplicitCurrentConfirmation(t *testing.T) {
	got, warnings, err := fuelModuleCaptureSuggestions("odometer", "123456,75", fuelUnitAttachmentID, Record{"odometer": "123457"})
	if err != nil || got["values"].(Record)["odometer"] != "123456.75" || !slices.Contains(fuelUnitWarningCodes(warnings), "odometer_decrease") {
		t.Fatalf("photo current comparison missing: %+v, %+v, %v", got, warnings, err)
	}
	for _, tc := range []struct {
		name    string
		input   Record
		invalid bool
	}{
		{"explicit date and reading", Record{"observed_on": "2026-10-01", "reading": "123456,750000", "notes": "Checked on the original"}, false},
		{"zero is real reading", Record{"observed_on": "2026-10-01", "reading": "0"}, false},
		{"date not invented", Record{"reading": "123456"}, true},
		{"invalid date", Record{"observed_on": "2026-02-29", "reading": "123456"}, true},
		{"unknown not zero", Record{"observed_on": "2026-10-01", "reading": nil}, true},
		{"JSON number refused", Record{"observed_on": "2026-10-01", "reading": 123456}, true},
		{"wrong photo field", Record{"observed_on": "2026-10-01", "reading": "123456", "total_minor": "1234"}, true},
		{"unknown nested field", Record{"observed_on": "2026-10-01", "reading": "123456", "confirmed": true}, true},
		{"new notes character cap", Record{"observed_on": "2026-10-01", "reading": "123456", "notes": strings.Repeat("a", 10001)}, true},
		{"new notes byte cap", Record{"observed_on": "2026-10-01", "reading": "123456", "notes": strings.Repeat("界", 7000)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, _, err := fuelModuleCaptureValues("odometer", tc.input)
			if (err != nil) != tc.invalid {
				t.Fatalf("photo values = %+v, %v; invalid=%v", values, err, tc.invalid)
			}
			if !tc.invalid {
				want := "123456.75"
				if tc.input["reading"] == "0" {
					want = "0"
				}
				if values["reading"] != want || values["observed_on"] != "2026-10-01" {
					t.Fatalf("actual reviewed reading/date changed: %+v", values)
				}
			}
		})
	}
}

func TestFuelModuleCaptureReceiptUsesFullTypedFactContract(t *testing.T) {
	input := fuelUnitFillup()
	input["quantity"], input["total_minor"] = "3", "100"
	got, warnings, err := fuelModuleCaptureValues("receipt", input)
	if err != nil || got["total_minor"] != "100" || got["unit_price"] != "0.333333" || !slices.Contains(fuelUnitWarningCodes(warnings), "price_rounding") {
		t.Fatalf("receipt bypassed exact manual fact contract: %+v, %+v, %v", got, warnings, err)
	}
	for _, invalid := range []any{nil, []string{"100"}, "text", Record{"reading": "1234"}, Record{"caller_ocr_text": "forged"}} {
		if values, _, err := fuelModuleCaptureValues("receipt", invalid); err == nil {
			t.Fatalf("unreviewed receipt shape accepted: %+v", values)
		}
	}
	delete(input, "full_tank")
	if _, _, err := fuelModuleCaptureValues("receipt", input); err == nil {
		t.Fatal("capture invented full-tank status from OCR")
	}
	if _, _, err := fuelModuleCaptureValues("unknown", Record{}); err == nil {
		t.Fatal("unknown capture type accepted")
	}
}

func TestFuelModuleCaptureResourceBoundsAndCandidateEvidence(t *testing.T) {
	for _, tc := range []struct{ name, kind, text, attachment string }{
		{"type", "invoice", "", fuelUnitAttachmentID}, {"attachment", "receipt", "", "bad"},
		{"bytes", "receipt", strings.Repeat("a", (64<<10)+1), fuelUnitAttachmentID},
		{"UTF8", "receipt", string([]byte{0xff}), fuelUnitAttachmentID}, {"NUL", "receipt", "a\x00b", fuelUnitAttachmentID},
		{"lines", "receipt", strings.Repeat("\n", 1000), fuelUnitAttachmentID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := fuelModuleCaptureSuggestions(tc.kind, tc.text, tc.attachment, Record{}); err == nil {
				t.Fatal("unbounded or malformed OCR source accepted")
			}
		})
	}
	lines := make([]string, 20)
	for index := range lines {
		lines[index] = "KM-Stand " + strconv.Itoa(index+1)
	}
	got, warnings, err := fuelModuleCaptureSuggestions("odometer", strings.Join(lines, "\n"), fuelUnitAttachmentID, Record{})
	if err != nil || len(got["candidates"].(map[string][]fuelCaptureCandidate)["odometer"]) != 16 || got["values"].(Record)["odometer"] != nil || !slices.Contains(fuelUnitWarningCodes(warnings), "ocr_ambiguous_odometer") {
		t.Fatalf("candidate limit or ambiguity lost: %+v, %+v, %v", got, warnings, err)
	}
	text := "KM-Stand 1234 " + strings.Repeat("ä", 180)
	got, _, err = fuelModuleCaptureSuggestions("odometer", text, fuelUnitAttachmentID, Record{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := got["candidates"].(map[string][]fuelCaptureCandidate)["odometer"][0].Evidence
	if !utf8.ValidString(evidence) || utf8.RuneCountInString(evidence) != 160 {
		t.Fatalf("evidence bound split UTF8 or exceeded visible snippet: %q", evidence)
	}
	got, _, err = fuelModuleCaptureSuggestions("odometer", strings.Repeat("a", 2001)+" KM-Stand 1234", fuelUnitAttachmentID, Record{})
	if err != nil || got["values"].(Record)["odometer"] != nil {
		t.Fatal("overlong OCR line mined for a partial reading", err)
	}
	for _, raw := range []any{Record{"notes": strings.Repeat("a", 128<<10)}, json.RawMessage(`{"observed_on":`)} {
		if _, _, err := fuelModuleCaptureValues("odometer", raw); err == nil {
			t.Fatal("oversized or malformed confirmed values accepted")
		}
	}
}
