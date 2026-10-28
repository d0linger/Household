package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const fuelUnitVehicleID = "a0000000-0000-4000-8000-000000000001"
const fuelUnitSeriesID = "b0000000-0000-4000-8000-000000000002"
const fuelUnitAttachmentID = "c0000000-0000-4000-8000-000000000003"

func fuelUnitRaw(t *testing.T, data Record) map[string]json.RawMessage {
	t.Helper()
	raw, err := fuelModuleRawValues(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fuelUnitFillup() Record {
	return Record{"odometer_series_id": fuelUnitSeriesID, "occurred_on": "2026-10-01", "fuel_type": "diesel", "odometer": "123456.123456",
		"quantity": "40", "quantity_unit": "l", "total_minor": "6800", "currency_code": "EUR", "full_tank": true, "previous_fill_missing": false}
}

func fuelUnitWarningCodes(warnings []FuelModuleWarning) []string {
	codes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		codes = append(codes, warning.Code)
	}
	slices.Sort(codes)
	return codes
}

func TestFuelModuleDecimalAndMinorBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		positive          bool
	}{
		{"zero measurement", "0", "0", false}, {"decimal comma", "12,340000", "12.34", true},
		{"precision edge", "999999999999.999999", "999999999999.999999", true},
		{"negative", "-1", "", false}, {"zero positive", "0", "", true}, {"negative fraction", "-0.000001", "", false},
		{"seventh place", "1.0000001", "", true}, {"wide integer", "1000000000000", "", true},
		{"fraction", "1/3", "", true}, {"exponent", "1e3", "", true}, {"nonfinite", "NaN", "", true},
		{"grouped number", "1,234.56", "", true}, {"unit suffix", "40 l", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fuelModuleDecimal(tc.input, tc.positive)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("invalid decimal %q accepted: %s", tc.input, got)
				}
				return
			}
			if err != nil || fuelModuleDecimalString(got) != tc.want {
				t.Fatalf("decimal %q = %v, %v; want %s", tc.input, got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		input    string
		positive bool
		want     int64
		invalid  bool
	}{
		{"0", false, 0, false}, {"001234", true, 1234, false}, {"1000000000000", true, 1_000_000_000_000, false},
		{"0", true, 0, true}, {"1000000000001", false, 0, true}, {"-1", false, 0, true}, {"12.34", true, 0, true},
		{"12,34", true, 0, true}, {"1e3", true, 0, true}, {" 1", true, 0, true}, {"", true, 0, true},
	} {
		t.Run("minor/"+tc.input, func(t *testing.T) {
			got, err := fuelModuleMinor(tc.input, tc.positive)
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("minor %q = %d, %v", tc.input, got, err)
			}
		})
	}
}

func TestFuelModulePriceExactDerivationAndResiduals(t *testing.T) {
	for _, tc := range []struct {
		name, quantity, price, total string
		want                         fuelPriceValues
		warning                      string
	}{
		{"derive total", "40", "1.7", "", fuelPriceValues{Quantity: "40", UnitPrice: "1.7", TotalMinor: "6800", DerivedField: "total_minor"}, ""},
		{"derive price", "40", "", "6800", fuelPriceValues{Quantity: "40", UnitPrice: "1.7", TotalMinor: "6800", DerivedField: "unit_price"}, ""},
		{"derive quantity", "", "1.7", "6800", fuelPriceValues{Quantity: "40", UnitPrice: "1.7", TotalMinor: "6800", DerivedField: "quantity"}, ""},
		{"all three preserve originals", "40.000", "1.700000", "006800", fuelPriceValues{Quantity: "40", UnitPrice: "1.7", TotalMinor: "6800"}, ""},
		{"half cent rounds upward", "1", "0.015", "", fuelPriceValues{Quantity: "1", UnitPrice: "0.015", TotalMinor: "2", DerivedField: "total_minor", RoundingDeltaMinor: "1/2"}, "price_rounding"},
		{"sub half cent rounds downward", "1", "0.014999", "", fuelPriceValues{Quantity: "1", UnitPrice: "0.014999", TotalMinor: "1", DerivedField: "total_minor", RoundingDeltaMinor: "-4999/10000"}, "price_rounding"},
		{"repeating derived price", "3", "", "100", fuelPriceValues{Quantity: "3", UnitPrice: "0.333333", TotalMinor: "100", DerivedField: "unit_price", RoundingDeltaMinor: "1/10000"}, "price_rounding"},
		{"repeating derived quantity", "", "3", "100", fuelPriceValues{Quantity: "0.333333", UnitPrice: "3", TotalMinor: "100", DerivedField: "quantity", RoundingDeltaMinor: "1/10000"}, "price_rounding"},
		{"one cent discrepancy", "1", "1", "101", fuelPriceValues{Quantity: "1", UnitPrice: "1", TotalMinor: "101", RoundingDeltaMinor: "1"}, "price_mismatch"},
		{"negative discrepancy", "1", "1", "99", fuelPriceValues{Quantity: "1", UnitPrice: "1", TotalMinor: "99", RoundingDeltaMinor: "-1"}, "price_mismatch"},
		{"integer cents stay cents", "2", "", "1234", fuelPriceValues{Quantity: "2", UnitPrice: "6.17", TotalMinor: "1234", DerivedField: "unit_price"}, ""},
		{"maximum accepted cents", "1000000", "10000", "", fuelPriceValues{Quantity: "1000000", UnitPrice: "10000", TotalMinor: "1000000000000", DerivedField: "total_minor"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := fuelModulePrice(tc.quantity, tc.price, tc.total)
			if err != nil || got != tc.want {
				t.Fatalf("price = %+v, %v; want %+v", got, err, tc.want)
			}
			wantCodes := []string{}
			if tc.warning != "" {
				wantCodes = append(wantCodes, tc.warning)
				if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "exakt "+tc.want.RoundingDeltaMinor) {
					t.Fatalf("exact residual missing from review: %+v", warnings)
				}
			}
			if !reflect.DeepEqual(fuelUnitWarningCodes(warnings), wantCodes) {
				t.Fatalf("warnings = %+v; want %v", warnings, wantCodes)
			}
			// A signed normalized review supplies all three values. That must keep
			// cents and required warning codes stable on acknowledgement/execution.
			again, nextWarnings, err := fuelModulePrice(got.Quantity, got.UnitPrice, got.TotalMinor)
			if err != nil || again.Quantity != got.Quantity || again.UnitPrice != got.UnitPrice || again.TotalMinor != got.TotalMinor || again.RoundingDeltaMinor != got.RoundingDeltaMinor || !reflect.DeepEqual(fuelUnitWarningCodes(nextWarnings), wantCodes) {
				t.Fatalf("normalized review drifted: %+v, %+v, %v", again, nextWarnings, err)
			}
		})
	}
}

func TestFuelModulePriceRejectsUnsafeOrInsufficientValues(t *testing.T) {
	for _, tc := range []struct{ name, quantity, price, total string }{
		{"none", "", "", ""}, {"quantity only", "1", "", ""}, {"price only", "", "1", ""}, {"total only", "", "", "100"},
		{"zero quantity", "0", "1", ""}, {"zero price", "1", "0", ""}, {"zero paid fillup", "1", "1", "0"},
		{"JSON money spelling", "1", "", "1.00"}, {"overflow total", "100000000", "100000000", ""},
		{"derived zero total", "0.000001", "0.000001", ""}, {"derived quantity beyond limit", "", "0.000001", "1000000000000"},
		{"derived price beyond limit", "0.000001", "", "1000000000000"}, {"quantity precision", "1.0000001", "1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _, err := fuelModulePrice(tc.quantity, tc.price, tc.total); err == nil {
				t.Fatalf("unsafe price accepted: %+v", got)
			}
		})
	}
}

func TestFuelModuleRoundedIntegerUsesExactHalfAwayFromZero(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"1/2", "1"}, {"-1/2", "-1"}, {"149/100", "1"}, {"-149/100", "-1"}, {"3/2", "2"}, {"-3/2", "-2"}, {"9007199254740993", "9007199254740993"}} {
		n, ok := new(big.Rat).SetString(tc.input)
		if !ok {
			t.Fatal("invalid fixture")
		}
		if got := fuelModuleRoundedInteger(n).String(); got != tc.want || n.RatString() != tc.input {
			t.Fatalf("round %s = %s; want %s, original = %s", tc.input, got, tc.want, n)
		}
	}
}

func TestFuelModuleNormalizeFillupIsClosedAndIdempotent(t *testing.T) {
	input := fuelUnitFillup()
	input["odometer_series_id"] = strings.ToUpper(fuelUnitSeriesID)
	input["quantity"], input["total_minor"] = "3", "100"
	input["acknowledged_warnings"] = []string{"price_rounding", "price_rounding", "quantity_outlier"}
	raw := fuelUnitRaw(t, input)
	original, _ := json.Marshal(raw)
	got, warnings, err := fuelModuleNormalizeInput("fillup.save", raw)
	if err != nil || got["total_minor"] != "100" || got["unit_price"] != "0.333333" || got["odometer_series_id"] != fuelUnitSeriesID || !reflect.DeepEqual(got["acknowledged_warnings"], []string{"price_rounding", "quantity_outlier"}) {
		t.Fatalf("normalization = %+v, %v", got, err)
	}
	again, againWarnings, err := fuelModuleNormalizeInput("fillup.save", fuelUnitRaw(t, got))
	if err != nil || !reflect.DeepEqual(got, again) || !reflect.DeepEqual(warnings, againWarnings) {
		t.Fatalf("review not stable: %+v, %+v, %v", again, againWarnings, err)
	}
	after, _ := json.Marshal(raw)
	if !bytes.Equal(original, after) {
		t.Fatal("normalization mutated caller input")
	}
}

func TestFuelModuleNormalizeRejectsMalformedFieldsAndReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]json.RawMessage)
	}{
		{"unknown field", func(in map[string]json.RawMessage) { in["amount_minor"] = json.RawMessage(`"6800"`) }},
		{"money JSON number", func(in map[string]json.RawMessage) { in["total_minor"] = json.RawMessage(`6800`) }},
		{"quantity JSON number", func(in map[string]json.RawMessage) { in["quantity"] = json.RawMessage(`40`) }},
		{"invalid UUID", func(in map[string]json.RawMessage) { in["odometer_series_id"] = json.RawMessage(`"../../vehicle"`) }},
		{"impossible date", func(in map[string]json.RawMessage) { in["occurred_on"] = json.RawMessage(`"2026-02-29"`) }},
		{"foreign dimension", func(in map[string]json.RawMessage) { in["quantity_unit"] = json.RawMessage(`"kg"`) }},
		{"nonASCII currency", func(in map[string]json.RawMessage) { in["currency_code"] = json.RawMessage(`"ÉUR"`) }},
		{"lower currency", func(in map[string]json.RawMessage) { in["currency_code"] = json.RawMessage(`"eur"`) }},
		{"missing explicit full", func(in map[string]json.RawMessage) { delete(in, "full_tank") }},
		{"null full", func(in map[string]json.RawMessage) { in["full_tank"] = json.RawMessage(`null`) }},
		{"string missing flag", func(in map[string]json.RawMessage) { in["previous_fill_missing"] = json.RawMessage(`"false"`) }},
		{"NUL notes", func(in map[string]json.RawMessage) { in["notes"] = json.RawMessage(`"a\u0000b"`) }},
		{"legacy notes byte cap", func(in map[string]json.RawMessage) {
			b, _ := json.Marshal(strings.Repeat("ä", 10001))
			in["notes"] = b
		}},
		{"too many ACKs", func(in map[string]json.RawMessage) {
			b, _ := json.Marshal(make([]string, 21))
			in["acknowledged_warnings"] = b
		}},
		{"padded ACK", func(in map[string]json.RawMessage) {
			in["acknowledged_warnings"] = json.RawMessage(`[" price_mismatch"]`)
		}},
		{"overlong ACK", func(in map[string]json.RawMessage) {
			b, _ := json.Marshal([]string{strings.Repeat("x", 81)})
			in["acknowledged_warnings"] = b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fuelUnitRaw(t, fuelUnitFillup())
			tc.edit(in)
			if got, _, err := fuelModuleNormalizeInput("fillup.save", in); err == nil {
				t.Fatalf("malformed input accepted: %+v", got)
			}
		})
	}
}

func TestFuelModuleDimensionAndChargingUnknownCost(t *testing.T) {
	for _, tc := range []struct{ fuel, unit string }{{"diesel", "l"}, {"petrol", "l"}, {"lpg", "l"}, {"cng", "kg"}, {"hydrogen", "kg"}, {"electric", "kWh"}} {
		if err := fuelModuleQuantityUnit(tc.fuel, tc.unit); err != nil {
			t.Fatalf("valid dimension %s/%s: %v", tc.fuel, tc.unit, err)
		}
		for _, wrong := range []string{"", "L", "kwh", "gal", "EUR"} {
			if fuelModuleQuantityUnit(tc.fuel, wrong) == nil {
				t.Fatalf("invalid dimension %s/%s accepted", tc.fuel, wrong)
			}
		}
	}
	base := Record{"occurred_on": "2026-10-01", "energy_kwh": "10.5", "measurement_basis": "unknown", "currency_code": "EUR", "charge_kind": "home"}
	for _, tc := range []struct {
		name string
		cost any
	}{{"unknown", nil}, {"free", "0"}, {"paid", "1234"}} {
		t.Run(tc.name, func(t *testing.T) {
			in := Record{}
			for name, entry := range base {
				in[name] = entry
			}
			in["total_minor"] = tc.cost
			got, _, err := fuelModuleNormalizeInput("charge.save", fuelUnitRaw(t, in))
			if err != nil || got["total_minor"] != tc.cost || got["measurement_basis"] != "unknown" {
				t.Fatalf("cost unknown/free distinction lost: %+v, %v", got, err)
			}
		})
	}
	for _, tc := range []struct {
		field string
		entry any
	}{
		{"measurement_basis", "wall_and_battery"}, {"charge_kind", "dc"}, {"soc_start", "100.01"}, {"soc_start", "1.001"},
		{"soc_start", "-1"}, {"odometer", "1000"}, {"odometer_series_id", fuelUnitSeriesID}, {"total_minor", "-1"},
	} {
		in := Record{}
		for name, entry := range base {
			in[name] = entry
		}
		in[tc.field] = tc.entry
		if got, _, err := fuelModuleNormalizeInput("charge.save", fuelUnitRaw(t, in)); err == nil {
			t.Fatalf("invalid charge %s=%v accepted: %+v", tc.field, tc.entry, got)
		}
	}
	fillup := fuelUnitFillup()
	fillup["fuel_type"], fillup["quantity_unit"] = "electric", "kWh"
	if _, _, err := fuelModuleNormalizeInput("fillup.save", fuelUnitRaw(t, fillup)); err == nil {
		t.Fatal("electricity mixed into tank fillups")
	}
}

func TestFuelModulePlausibilityPreservesDimensionsAndExactThresholds(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		data, previous, settings Record
		codes                    []string
	}{
		{"at quantity bound", Record{"quantity": "50", "quantity_unit": "l"}, Record{}, Record{"maximum_fill_quantity": "50", "plausibility_unit": "l"}, []string{}},
		{"one micro above", Record{"quantity": "50.000001", "quantity_unit": "l"}, Record{}, Record{"maximum_fill_quantity": "50", "plausibility_unit": "l"}, []string{"quantity_outlier"}},
		{"wrong dimension", Record{"quantity": "500", "quantity_unit": "kg"}, Record{}, Record{"maximum_fill_quantity": "50", "plausibility_unit": "l"}, []string{}},
		{"wrong price currency", Record{"unit_price": "99", "quantity_unit": "l", "currency_code": "USD"}, Record{}, Record{"maximum_unit_price": "2", "plausibility_unit": "l", "plausibility_currency_code": "EUR"}, []string{}},
		{"same price currency", Record{"unit_price": "2.000001", "quantity_unit": "l", "currency_code": "EUR"}, Record{}, Record{"maximum_unit_price": "2", "plausibility_unit": "l", "plausibility_currency_code": "EUR"}, []string{"price_outlier"}},
		{"unknown not zero", Record{"quantity": nil, "quantity_unit": "l"}, Record{}, Record{"minimum_consumption": "2", "plausibility_unit": "l"}, []string{}},
		{"low exact consumption", Record{"consumption": "1.999999", "quantity_unit": "l"}, Record{}, Record{"minimum_consumption": "2", "plausibility_unit": "l"}, []string{"consumption_low"}},
		{"odometer falls", Record{"odometer": "99.999999"}, Record{"odometer": "100"}, Record{}, []string{"odometer_decrease"}},
		{"daily exact limit", Record{"odometer": "300", "occurred_on": "2026-10-03"}, Record{"odometer": "100", "occurred_on": "2026-10-01"}, Record{"maximum_daily_distance": "100"}, []string{}},
		{"daily above limit", Record{"odometer": "300.000001", "occurred_on": "2026-10-03"}, Record{"odometer": "100", "occurred_on": "2026-10-01"}, Record{"maximum_daily_distance": "100"}, []string{"distance_outlier"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.data)
			got := fuelModulePlausibility(tc.data, tc.previous, tc.settings)
			if !reflect.DeepEqual(fuelUnitWarningCodes(got), tc.codes) {
				t.Fatalf("warnings = %+v; want %v", got, tc.codes)
			}
			after, _ := json.Marshal(tc.data)
			if !bytes.Equal(before, after) {
				t.Fatal("plausibility mutated original facts")
			}
		})
	}
	settings := Record{"plausibility_unit": "kWh", "plausibility_currency_code": "EUR", "maximum_fill_quantity": "20", "maximum_unit_price": "0.333333"}
	for _, tc := range []struct {
		name  string
		cost  any
		codes []string
	}{
		{"unknown", nil, []string{}}, {"free", "0", []string{}}, {"exact repeating price", "100", []string{"price_outlier"}},
	} {
		t.Run("charging/"+tc.name, func(t *testing.T) {
			got := fuelModuleChargePlausibility(Record{"energy_kwh": "3", "currency_code": "EUR", "total_minor": tc.cost}, settings)
			if !reflect.DeepEqual(fuelUnitWarningCodes(got), tc.codes) {
				t.Fatalf("charge price rounded before comparison: %+v", got)
			}
		})
	}
}

func TestFuelModuleTripReplacementPreferencesAndVehicleControls(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		data         Record
		invalid      bool
	}{
		{"explicit archive", "vehicle.archive", Record{"archived": true}, false},
		{"explicit restore", "vehicle.archive", Record{"archived": false}, false},
		{"missing archive decision", "vehicle.archive", Record{}, true},
		{"null archive decision", "vehicle.archive", Record{"archived": nil}, true},
		{"select existing series", "vehicle.select_series", Record{"odometer_series_id": strings.ToUpper(fuelUnitSeriesID), "series_version": int64(1)}, false},
		{"select missing version", "vehicle.select_series", Record{"odometer_series_id": fuelUnitSeriesID}, true},
		{"select string version", "vehicle.select_series", Record{"odometer_series_id": fuelUnitSeriesID, "series_version": "1"}, true},
		{"trip actual distance", "trip.save", Record{"odometer_series_id": fuelUnitSeriesID, "started_on": "2026-10-01", "ended_on": "2026-10-02", "start_odometer": "100.000001", "end_odometer": "100.000002", "purpose": "Appointment"}, false},
		{"trip stationary", "trip.save", Record{"odometer_series_id": fuelUnitSeriesID, "started_on": "2026-10-01", "ended_on": "2026-10-01", "start_odometer": "100", "end_odometer": "100", "purpose": "Appointment"}, true},
		{"trip falls", "trip.save", Record{"odometer_series_id": fuelUnitSeriesID, "started_on": "2026-10-01", "ended_on": "2026-10-02", "start_odometer": "101", "end_odometer": "100", "purpose": "Appointment"}, true},
		{"trip date reversed", "trip.save", Record{"odometer_series_id": fuelUnitSeriesID, "started_on": "2026-10-02", "ended_on": "2026-10-01", "start_odometer": "100", "end_odometer": "101", "purpose": "Appointment"}, true},
		{"replacement resets reading explicitly", "odometer.replace", Record{"closed_on": "2026-10-01", "final_reading": "123456", "started_on": "2026-10-01", "initial_reading": "0", "reason": "Physical odometer replacement"}, false},
		{"replacement date reversed", "odometer.replace", Record{"closed_on": "2026-10-02", "final_reading": "123456", "started_on": "2026-10-01", "initial_reading": "0", "reason": "Physical odometer replacement"}, true},
		{"defaults vehicle and series", "settings.save", Record{"default_vehicle_id": fuelUnitVehicleID, "default_series_id": fuelUnitSeriesID, "default_station": "Local"}, false},
		{"series without vehicle", "settings.save", Record{"default_series_id": fuelUnitSeriesID}, true},
		{"explicit dimension limits", "settings.save", Record{"minimum_consumption": "2", "maximum_consumption": "20", "maximum_unit_price": "2.5", "plausibility_unit": "l", "plausibility_currency_code": "EUR"}, false},
		{"dimension absent", "settings.save", Record{"maximum_fill_quantity": "80"}, true},
		{"price currency absent", "settings.save", Record{"maximum_unit_price": "2.5", "plausibility_unit": "l"}, true},
		{"inverted limits", "settings.save", Record{"minimum_consumption": "20", "maximum_consumption": "2", "plausibility_unit": "l"}, true},
		{"delete has no hidden mutation fields", "fillup.delete", Record{"quantity": "40"}, true},
		{"unknown action", "fuel.fake", Record{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := fuelModuleNormalizeInput(tc.action, fuelUnitRaw(t, tc.data))
			if (err != nil) != tc.invalid {
				t.Fatalf("action %s = %+v, %v; invalid=%v", tc.action, got, err, tc.invalid)
			}
		})
	}
}

func TestFuelModulePhysicalScopeUsesSelectedHistoricalSeries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(Record, Record, Record)
		day     string
		invalid bool
	}{
		{"initial boundary", func(_, _, p Record) { p["odometer"] = "100" }, "2026-01-01", false},
		{"final boundary", func(_, _, p Record) { p["odometer"] = "500" }, "2026-06-30", false},
		{"before series", func(_, _, _ Record) {}, "2025-12-31", true},
		{"after series", func(_, _, _ Record) {}, "2026-07-01", true},
		{"below initial", func(_, _, p Record) { p["odometer"] = "99.999999" }, "2026-02-01", true},
		{"above final", func(_, _, p Record) { p["odometer"] = "500.000001" }, "2026-02-01", true},
		{"archived vehicle", func(v, _, _ Record) { v["archived_at"] = "2026-01-01" }, "2026-02-01", true},
		{"different vehicle", func(_, s, _ Record) { s["vehicle_id"] = fuelUnitAttachmentID }, "2026-02-01", true},
		{"different selected series", func(_, _, p Record) { p["odometer_series_id"] = fuelUnitAttachmentID }, "2026-02-01", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vehicle := Record{"id": fuelUnitVehicleID}
			series := Record{"id": fuelUnitSeriesID, "vehicle_id": fuelUnitVehicleID, "started_on": "2026-01-01", "ended_on": "2026-06-30", "initial_reading": "100", "final_reading": "500"}
			proposed := Record{"odometer_series_id": fuelUnitSeriesID, "odometer": "200"}
			tc.edit(vehicle, series, proposed)
			if err := fuelModulePhysicalScope(vehicle, series, proposed, tc.day); (err != nil) != tc.invalid {
				t.Fatalf("scope = %v; invalid=%v", err, tc.invalid)
			}
		})
	}
}

func TestFuelModuleReviewIdentityDigestAndACK(t *testing.T) {
	base := FuelModuleCommand{Action: "fillup.save", Generation: 1, VehicleID: strings.ToUpper(fuelUnitVehicleID), SourceStamp: strings.Repeat("ab", 32), Data: fuelUnitRaw(t, fuelUnitFillup())}
	if err := fuelModuleCommandIdentity(base); err != nil {
		t.Fatalf("valid UUID case refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*FuelModuleCommand)
	}{
		{"generation", func(c *FuelModuleCommand) { c.Generation = 0 }}, {"version", func(c *FuelModuleCommand) { c.Version = -1 }},
		{"vehicle", func(c *FuelModuleCommand) { c.VehicleID = "bad" }}, {"record", func(c *FuelModuleCommand) { c.RecordID = "bad" }},
		{"stamp case", func(c *FuelModuleCommand) { c.SourceStamp = strings.Repeat("AB", 32) }}, {"stamp length", func(c *FuelModuleCommand) { c.SourceStamp = "ab" }},
		{"stamp hex", func(c *FuelModuleCommand) { c.SourceStamp = strings.Repeat("xy", 32) }}, {"private hash", func(c *FuelModuleCommand) { c.legacyHash = []byte{1} }},
		{"unknown dependency", func(c *FuelModuleCommand) { c.TargetGenerations = map[string]int64{"shopping": 1} }}, {"dependency generation", func(c *FuelModuleCommand) { c.TargetGenerations = map[string]int64{"expenses": 0} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.edit(&in)
			if fuelModuleCommandIdentity(in) == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
	hash, err := fuelModuleCommandDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.SourceStamp = strings.Repeat("cd", 32)
	next, _ := fuelModuleCommandDigest(changed)
	if bytes.Equal(hash, next) {
		t.Fatal("fresh operation digest ignored reviewed source state")
	}
	private := sha256.Sum256([]byte("legacy intent"))
	base.legacyHash, changed.legacyHash = private[:], private[:]
	hash, _ = fuelModuleCommandDigest(base)
	next, _ = fuelModuleCommandDigest(changed)
	if !bytes.Equal(hash, next) {
		t.Fatal("legacy durable replay identity changed with own new source state")
	}
	encoded, _ := json.Marshal(base)
	if strings.Contains(string(encoded), "legacyHash") || strings.Contains(string(encoded), "legacy_hash") {
		t.Fatal("private replay override leaked onto public command")
	}
	warnings := []FuelModuleWarning{{Code: "price_mismatch"}, {Code: "quantity_outlier"}}
	if fuelModuleAcknowledge(warnings, Record{"acknowledged_warnings": []string{"price_mismatch"}}) == nil {
		t.Fatal("partial acknowledgement accepted")
	}
	if err := fuelModuleAcknowledge(warnings, Record{"acknowledged_warnings": []string{"quantity_outlier", "price_mismatch"}}); err != nil {
		t.Fatalf("complete acknowledgement refused: %v", err)
	}
}

func TestFuelModuleReceiptDecoderAndWireExactness(t *testing.T) {
	var response struct {
		Data Record `json:"data"`
	}
	if err := fuelModuleDecodeResponse([]byte(`{"data":{"odometer":9007199254740993,"total_minor":1234}}`), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response.Data["odometer"].(json.Number); !ok {
		t.Fatal("database receipt numeric value passed through float64")
	}
	got := fuelModuleWireRecord(response.Data)
	if got["odometer"] != "9007199254740993" || got["total_minor"] != "1234" {
		t.Fatalf("wire exact strings lost: %+v", got)
	}
	for _, raw := range []string{"", `{"unexpected":true}`, `{"data":{}} {}`, `{"data":`, strings.Repeat(" ", (256<<10)+1)} {
		if err := fuelModuleDecodeResponse([]byte(raw), &response); !errors.Is(err, ErrConflict) {
			t.Fatalf("malformed receipt accepted: %q, %v", raw[:min(len(raw), 80)], err)
		}
	}
}
