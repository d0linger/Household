package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const fuelUnitCSVHeader = "occurred_on,odometer,quantity,total_minor,full_tank,previous_fill_missing,station,notes\n"
const fuelUnitCSVRow = "2026-10-01,1000,40,6800,true,false,Station,Original\n"

func fuelUnitImportOptions() Record {
	return Record{"fuel_type": "diesel", "quantity_unit": "l", "currency_code": "EUR"}
}

func fuelUnitImportRows(t *testing.T, parsed Record) []Record {
	t.Helper()
	rows, ok := parsed["rows"].([]Record)
	if !ok {
		t.Fatalf("unexpected parsed rows: %#v", parsed)
	}
	return rows
}

func fuelUnitImportFields() map[string]string {
	return map[string]string{"occurred_on": "2026-10-01", "odometer": "1000", "quantity": "40", "total_minor": "6800", "full_tank": "1", "previous_fill_missing": "0"}
}

func fuelUnitImportValueOptions() Record {
	return Record{"date_format": "iso", "decimal_separator": ".", "distance_unit": "km", "fuel_type": "diesel", "quantity_unit": "l", "currency_code": "EUR"}
}

func TestFuelModuleImportProfilesRequireTruthfulMapping(t *testing.T) {
	profiles := fuelModuleImportProfiles()
	if len(profiles) != 4 {
		t.Fatalf("profiles = %+v", profiles)
	}
	for _, profile := range profiles {
		id := String(profile["id"])
		if id == "drivvo" || id == "spritmonitor" {
			if profile["mapping_required"] != true || profile["native_log"] != false {
				t.Fatalf("unverified native format advertised: %+v", profile)
			}
			if _, _, err := fuelModuleImportParse(t.Context(), id, fuelUnitCSVHeader+fuelUnitCSVRow, fuelUnitImportOptions()); err == nil {
				t.Fatalf("%s silently guessed export columns", id)
			}
		}
	}
	for _, profile := range []string{"drivvo", "spritmonitor"} {
		t.Run(profile, func(t *testing.T) {
			options := fuelUnitImportValueOptions()
			options["delimiter"], options["decimal_separator"], options["date_format"] = ";", ",", "dmy"
			options["columns"] = map[string]string{"occurred_on": "Datum", "odometer": "Kilometer", "quantity": "Liter", "total": "EUR", "full_tank": "Voll", "previous_fill_missing": "Fehlt"}
			parsed, _, err := fuelModuleImportParse(t.Context(), profile, "Datum;Kilometer;Liter;EUR;Voll;Fehlt\n01.10.2026;1234,5;40,25;68,00;ja;nein\n", fuelUnitImportFlagOptions(options))
			if err != nil {
				t.Fatal(err)
			}
			row := fuelUnitImportRows(t, parsed)[0]
			values, ok := row["values"].(Record)
			if !ok || values["odometer"] != "1234.5" || values["quantity"] != "40.25" || values["total_minor"] != "6800" || values["full_tank"] != true || values["previous_fill_missing"] != false {
				t.Fatalf("explicit mapping failed: %+v", row)
			}
		})
	}
}

func fuelUnitImportFlagOptions(options Record) Record {
	options["full_tank_values"] = map[string]bool{"ja": true, "nein": false}
	options["missing_values"] = map[string]bool{"ja": true, "nein": false}
	return options
}

func TestFuelModuleHouseholdImportPreservesSourceAndDuplicateHints(t *testing.T) {
	text := "\ufeff" + fuelUnitCSVHeader + fuelUnitCSVRow + strings.Replace(fuelUnitCSVRow, "Station,Original", "Other station,Changed note", 1)
	parsed, _, err := fuelModuleImportParse(t.Context(), "household", text, fuelUnitImportOptions())
	if err != nil {
		t.Fatal(err)
	}
	rows := fuelUnitImportRows(t, parsed)
	hash := sha256.Sum256([]byte(text))
	if parsed["source_sha256"] != hex.EncodeToString(hash[:]) || parsed["requires_confirmation"] != true || len(rows) != 2 {
		t.Fatalf("source identity lost: %+v", parsed)
	}
	first, second := rows[0], rows[1]
	if first["row_number"] != "1" || first["source_line"] != "2" || second["row_number"] != "2" || second["source_line"] != "3" {
		t.Fatalf("logical and physical positions wrong: %+v", rows)
	}
	if first["fingerprint"] == second["fingerprint"] || first["duplicate_fingerprint"] != second["duplicate_fingerprint"] || second["duplicate_of_row"] != "1" || second["decision"] != "review_duplicate" || first["decision"] != "pending" {
		t.Fatalf("source identity and physical duplicate hint conflated: %+v", rows)
	}
	evidence := second["source_values"].(Record)
	if evidence["duplicate_of_row"] != "1" || evidence["line"] != "3" || evidence["fields"].(map[string]string)["notes"] != "Changed note" {
		t.Fatalf("persistable source evidence missing: %+v", evidence)
	}
	again, _, err := fuelModuleImportParse(t.Context(), "household", text, fuelUnitImportOptions())
	if err != nil || !reflect.DeepEqual(parsed, again) {
		t.Fatal("same source and declaration did not produce deterministic review", err)
	}
	changed, _, err := fuelModuleImportParse(t.Context(), "household", strings.TrimPrefix(text, "\ufeff"), fuelUnitImportOptions())
	if err != nil || fuelUnitImportRows(t, changed)[0]["fingerprint"] == first["fingerprint"] {
		t.Fatal("source-byte change reused source fingerprint", err)
	}
}

func TestFuelModuleImportUsesPhysicalCSVLineForMultilineEvidence(t *testing.T) {
	text := fuelUnitCSVHeader + "2026-10-01,1000,40,6800,true,false,Station,\"line one\nline two\"\n\n" + fuelUnitCSVRow
	parsed, _, err := fuelModuleImportParse(t.Context(), "household", text, fuelUnitImportOptions())
	if err != nil {
		t.Fatal(err)
	}
	rows := fuelUnitImportRows(t, parsed)
	if len(rows) != 2 || rows[1]["source_line"] != "5" || rows[1]["row_number"] != "2" || rows[0]["values"].(Record)["notes"] != "line one\nline two" {
		t.Fatalf("CSV record count mistaken for physical provenance: %+v", rows)
	}
}

func TestFuelModuleFuelioNativeLogAndContinuityDeclarations(t *testing.T) {
	const prefix = "## Vehicle\nName,DistUnit,FuelUnit,ImportCSVDateFormat,TankCount\nCar,km,l,yyyy-MM-dd,1\n\n## Log\n"
	const header = "Data,Odo (km),Fuel (l),Full,Price (optional),Missed,TankNumber\n"
	const row = "2026-10-01,1000,40,1,68.00,0,1\n"
	text := prefix + header + row + "## Costs\nDate,Cost\n2026-10-01,999\n"
	options := fuelUnitImportOptions()
	options["history_complete"] = false
	parsed, warnings, err := fuelModuleImportParse(t.Context(), "fuelio", text, options)
	if err != nil {
		t.Fatal(err)
	}
	rows := fuelUnitImportRows(t, parsed)
	if len(rows) != 1 || rows[0]["source_line"] != "7" || rows[0]["values"].(Record)["previous_fill_missing"] != false || !slices.Contains(fuelUnitWarningCodes(warnings), "import_log_only") || parsed["source_metadata"].(Record)["Name"] != "Car" {
		t.Fatalf("native flags or log-only provenance lost: %+v, %+v", parsed, warnings)
	}
	for _, tc := range []struct {
		name        string
		declaration any
		missing     bool
		invalid     bool
	}{
		{"not declared", nil, false, true}, {"declared complete", true, false, false}, {"declared gaps unknown", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := fuelUnitImportOptions()
			if tc.declaration != nil {
				opts["history_complete"] = tc.declaration
			}
			withoutFlag := prefix + "Data,Odo (km),Fuel (l),Full,Price (optional)\n2026-10-01,1000,40,1,68.00\n"
			result, _, err := fuelModuleImportParse(t.Context(), "fuelio", withoutFlag, opts)
			if err != nil {
				t.Fatal(err)
			}
			r := fuelUnitImportRows(t, result)[0]
			if tc.invalid {
				if len(r["errors"].([]string)) != 1 || r["values"] != nil {
					t.Fatalf("undeclared continuity fabricated: %+v", r)
				}
			} else if r["values"].(Record)["previous_fill_missing"] != tc.missing {
				t.Fatalf("continuity declaration ignored: %+v", r)
			}
		})
	}
	if _, _, err := fuelModuleImportParse(t.Context(), "fuelio", prefix+header+row+"## Log\n"+header+row, fuelUnitImportOptions()); err == nil {
		t.Fatal("multiple native tank logs merged without explicit separation")
	}
	badTank, _, err := fuelModuleImportParse(t.Context(), "fuelio", prefix+header+strings.Replace(row, ",0,1\n", ",0,2\n", 1), fuelUnitImportOptions())
	if err != nil || len(fuelUnitImportRows(t, badTank)[0]["errors"].([]string)) != 1 {
		t.Fatal("second tank accepted into first tank series", err)
	}
	conflict := fuelUnitImportOptions()
	conflict["distance_unit"] = "mi"
	if _, _, err := fuelModuleImportParse(t.Context(), "fuelio", prefix+header+row, conflict); err == nil {
		t.Fatal("explicit unit selection contradicted native header without denial")
	}
}

func TestFuelModuleImportExactUnitsDatesCurrencyAndNativeFlags(t *testing.T) {
	for _, tc := range []struct {
		name, unit, distanceUnit, quantity, reading, wantQuantity, wantReading string
		codes                                                                  []string
	}{
		{"metric", "l", "km", "10", "100", "10", "100", []string{}},
		{"international mile", "l", "mi", "10", "1", "10", "1.609344", []string{"import_distance_conversion"}},
		{"US gallon", "us_gal", "km", "1", "100", "3.785412", "100", []string{"import_quantity_conversion"}},
		{"Imperial gallon", "imp_gal", "km", "1", "100", "4.54609", "100", []string{"import_quantity_conversion"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, options := fuelUnitImportFields(), fuelUnitImportValueOptions()
			fields["quantity"], fields["odometer"] = tc.quantity, tc.reading
			options["quantity_unit"], options["distance_unit"] = tc.unit, tc.distanceUnit
			values, warnings, err := fuelModuleImportValues(fields, options)
			if err != nil || values["quantity"] != tc.wantQuantity || values["odometer"] != tc.wantReading || values["quantity_unit"] != "l" || values["total_minor"] != "6800" {
				t.Fatalf("unit conversion = %+v, %v", values, err)
			}
			for _, code := range tc.codes {
				if !slices.Contains(fuelUnitWarningCodes(warnings), code) {
					t.Fatalf("unit conversion unannounced: %+v", warnings)
				}
			}
		})
	}
	for _, tc := range []struct{ name, date, layout, want string }{
		{"German leap date", "29.02.2024", "dmy", "2024-02-29"}, {"US explicit", "02/03/2026", "mdy-slash", "2026-02-03"}, {"EU explicit", "02/03/2026", "dmy-slash", "2026-03-02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, options := fuelUnitImportFields(), fuelUnitImportValueOptions()
			fields["occurred_on"], options["date_format"] = tc.date, tc.layout
			values, _, err := fuelModuleImportValues(fields, options)
			if err != nil || values["occurred_on"] != tc.want {
				t.Fatalf("explicit date = %+v, %v", values, err)
			}
		})
	}
	fields, options := fuelUnitImportFields(), fuelUnitImportValueOptions()
	fields["currency_code"] = "USD"
	options["full_tank_values"], options["missing_values"] = map[string]bool{"1": false}, map[string]bool{"0": true}
	got, _, err := fuelModuleImportValues(fields, options)
	if err != nil || got["currency_code"] != "USD" || got["total_minor"] != "6800" || got["full_tank"] != false || got["previous_fill_missing"] != true {
		t.Fatalf("source currency/explicit flag mapping replaced by guesses: %+v, %v", got, err)
	}
}

func TestFuelModuleImportRejectsAmbiguousSourceValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]string, Record)
	}{
		{"currency symbol", func(f map[string]string, _ Record) { f["currency_code"] = "$" }},
		{"missing currency", func(_ map[string]string, o Record) { delete(o, "currency_code") }},
		{"unselected date format", func(_ map[string]string, o Record) { delete(o, "date_format") }},
		{"ambiguous date", func(f map[string]string, _ Record) { f["occurred_on"] = "01/02/2026" }},
		{"invalid date", func(f map[string]string, _ Record) { f["occurred_on"] = "2026-02-29" }},
		{"unselected decimal", func(_ map[string]string, o Record) { delete(o, "decimal_separator") }},
		{"thousands separator", func(f map[string]string, _ Record) { f["odometer"] = "1,000" }},
		{"unknown gallon", func(_ map[string]string, o Record) { o["quantity_unit"] = "gal" }},
		{"dimension mismatch", func(_ map[string]string, o Record) { o["quantity_unit"] = "kg" }},
		{"electric not fillup", func(_ map[string]string, o Record) { o["fuel_type"], o["quantity_unit"] = "electric", "kWh" }},
		{"too precise money", func(f map[string]string, _ Record) { f["total"] = "68.001" }},
		{"conflicting money", func(f map[string]string, _ Record) { f["total"] = "68.01" }},
		{"unmapped full", func(f map[string]string, _ Record) { f["full_tank"] = "yes" }},
		{"missing full", func(f map[string]string, _ Record) { delete(f, "full_tank") }},
		{"not fuel record", func(f map[string]string, _ Record) { f["record_type"] = "maintenance" }},
		{"long notes", func(f map[string]string, _ Record) { f["notes"] = strings.Repeat("a", 20001) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, options := fuelUnitImportFields(), fuelUnitImportValueOptions()
			tc.edit(fields, options)
			if got, _, err := fuelModuleImportValues(fields, options); err == nil {
				t.Fatalf("ambiguous import accepted: %+v", got)
			}
		})
	}
}

func TestFuelModuleImportParseLimitsAndInvalidRowsKeepFingerprint(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"empty", ""}, {"over bytes", strings.Repeat("a", (2<<20)+1)}, {"invalid UTF8", string([]byte{0xff})}, {"NUL", "a\x00b"},
		{"duplicate header", "occurred_on,occurred_on\n1,2\n"}, {"empty header field", "occurred_on,,quantity\n1,2,3\n"},
		{"broken quote", fuelUnitCSVHeader + "\"unterminated\n"}, {"unknown column", "injected\nvalue\n"},
		{"over columns", strings.Repeat("column,", 80) + "last\n"}, {"over rows", fuelUnitCSVHeader + strings.Repeat(fuelUnitCSVRow, 1001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := fuelModuleImportParse(t.Context(), "household", tc.text, fuelUnitImportOptions()); err == nil {
				t.Fatal("invalid or unbounded CSV accepted")
			}
		})
	}
	parsed, _, err := fuelModuleImportParse(t.Context(), "household", fuelUnitCSVHeader+strings.Repeat(fuelUnitCSVRow, 1000), fuelUnitImportOptions())
	if err != nil || len(fuelUnitImportRows(t, parsed)) != 1000 {
		t.Fatal("bounded 1000-row import refused", err)
	}
	invalid, _, err := fuelModuleImportParse(t.Context(), "household", fuelUnitCSVHeader+"2026-10-01,1\n"+strings.Replace(fuelUnitCSVRow, "2026-10-01", "not a date", 1), fuelUnitImportOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range fuelUnitImportRows(t, invalid) {
		fingerprint, decodeErr := hex.DecodeString(String(row["fingerprint"]))
		if decodeErr != nil || len(fingerprint) != sha256.Size || len(row["errors"].([]string)) == 0 || row["values"] != nil || row["decision"] != "pending" {
			t.Fatalf("invalid row lost safe skip identity: %+v", row)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := fuelModuleImportParse(ctx, "household", fuelUnitCSVHeader+fuelUnitCSVRow, fuelUnitImportOptions()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled parsing kept processing: %v", err)
	}
}

func TestFuelModuleImportClosedMappingAndTypedStagePreserveSourceBytes(t *testing.T) {
	header := []string{"date", "reading", "litres", "money"}
	valid := map[string]string{"occurred_on": "date", "odometer": "reading", "quantity": "litres", "total_minor": "money"}
	if err := fuelModuleImportMapping(valid, header); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []map[string]string{
		{"occurred_on": "date", "odometer": "reading", "quantity": "litres"},
		{"occurred_on": "date", "odometer": "reading", "quantity": "litres", "total": "absent"},
		{"occurred_on": "date", "odometer": "reading", "quantity": "reading", "total_minor": "money"},
		{"occurred_on": "date", "odometer": "reading", "quantity": "litres", "total_minor": "money", "execute_sql": "date"},
	} {
		if fuelModuleImportMapping(invalid, header) == nil {
			t.Fatalf("invalid mapping accepted: %+v", invalid)
		}
	}
	text := "\n" + strings.ReplaceAll(fuelUnitCSVHeader+fuelUnitCSVRow, ",", "\t") + "\n"
	input := Record{"profile": "household", "csv_text": text, "delimiter": "\t", "currency_code": "EUR", "fuel_type": "diesel", "history_complete": false}
	got, _, err := fuelModuleNormalizeInput("import.stage", fuelUnitRaw(t, input))
	if err != nil || got["csv_text"] != text || got["delimiter"] != "\t" || got["history_complete"] != false {
		t.Fatalf("source bytes, tab or declaration mutated: %+v, %v", got, err)
	}
	got["quantity_unit"] = "l"
	parsed, _, err := fuelModuleImportParse(t.Context(), "household", String(got["csv_text"]), got)
	if err != nil || len(fuelUnitImportRows(t, parsed)) != 1 {
		t.Fatal("explicit tab CSV unusable after normalized review", err)
	}
}

func TestFuelModuleImportDecisionsBindEveryRowIncludingSkip(t *testing.T) {
	fingerprint := strings.Repeat("ab", 32)
	valid := []fuelImportDecision{{RowNumber: "1", Fingerprint: fingerprint, Decision: "import", DuplicateAck: true, SeriesID: fuelUnitSeriesID}, {RowNumber: "1000", Fingerprint: fingerprint, Decision: "skip"}}
	got, err := fuelModuleImportDecisions(valid)
	if err != nil || !reflect.DeepEqual(got, valid) {
		t.Fatalf("valid explicit import/skip choices refused: %+v, %v", got, err)
	}
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"empty", []fuelImportDecision{}}, {"null", nil}, {"not array", Record{"row_number": "1"}},
		{"unknown field", []Record{{"row_number": "1", "fingerprint": fingerprint, "decision": "skip", "force": true}}},
		{"skip without fingerprint", []fuelImportDecision{{RowNumber: "1", Decision: "skip"}}},
		{"fingerprint case", []fuelImportDecision{{RowNumber: "1", Fingerprint: strings.ToUpper(fingerprint), Decision: "skip"}}},
		{"row canonical", []fuelImportDecision{{RowNumber: "01", Fingerprint: fingerprint, Decision: "skip"}}},
		{"row out of range", []fuelImportDecision{{RowNumber: "1001", Fingerprint: fingerprint, Decision: "skip"}}},
		{"unknown decision", []fuelImportDecision{{RowNumber: "1", Fingerprint: fingerprint, Decision: "auto"}}},
		{"duplicate row", []fuelImportDecision{valid[0], valid[0]}},
		{"bad series", []fuelImportDecision{{RowNumber: "1", Fingerprint: fingerprint, Decision: "import", SeriesID: "bad"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fuelModuleImportDecisions(tc.raw); err == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
	choices := make([]fuelImportDecision, 101)
	for index := range choices {
		choices[index] = fuelImportDecision{RowNumber: strconv.Itoa(index + 1), Fingerprint: fingerprint, Decision: "skip"}
	}
	if _, err := fuelModuleImportDecisions(choices[:100]); err != nil {
		t.Fatal("bounded 100-row choice refused", err)
	}
	if _, err := fuelModuleImportDecisions(choices); err == nil {
		t.Fatal("over 100 decisions accepted")
	}
}

func TestFuelModuleImportFingerprintExcludesEditorialHintsOnly(t *testing.T) {
	data := fuelUnitFillup()
	before, err := fuelModuleImportFingerprint(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"station", "notes", "unit_price"} {
		data[field] = "different"
	}
	data["odometer_series_id"] = fuelUnitAttachmentID
	after, _ := fuelModuleImportFingerprint(data)
	if before != after {
		t.Fatal("editorial changes hid a possible duplicate")
	}
	for _, field := range []string{"occurred_on", "odometer", "fuel_type", "quantity", "quantity_unit", "total_minor", "currency_code"} {
		changed := fuelUnitFillup()
		changed[field] = "different"
		hash, _ := fuelModuleImportFingerprint(changed)
		if hash == before {
			t.Fatalf("physical identity field %s omitted from duplicate hint", field)
		}
	}
	// Nested option objects must remain JSON-compatible and bounded, rather than
	// allowing an unmarshal failure to silently fall back to guessed mappings.
	var output map[string]string
	if fuelModuleImportObject(json.RawMessage(`{"x":1}`), &output) == nil {
		t.Fatal("wrong typed mapping silently coerced")
	}
}
