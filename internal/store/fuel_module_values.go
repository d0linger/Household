package store

import (
	"encoding/json"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/value"
)

const fuelModuleMaxMinor int64 = 1_000_000_000_000

var fuelMinorPattern = regexp.MustCompile(`^[0-9]{1,13}$`)
var fuelCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type fuelPriceValues struct {
	Quantity           string `json:"quantity"`
	UnitPrice          string `json:"unit_price"`
	TotalMinor         string `json:"total_minor"`
	DerivedField       string `json:"derived_field,omitempty"`
	RoundingDeltaMinor string `json:"rounding_delta_minor,omitempty"`
}

// fuelModuleDecimal accepts only a bounded ordinary decimal, never an exponent,
// fraction, floating point JSON number, implicit thousands separator or unit.
func fuelModuleDecimal(text string, positive bool) (*big.Rat, error) {
	number, err := value.Decimal(text)
	if err != nil || number.Sign() < 0 || (positive && number.Sign() == 0) {
		return nil, Invalid{"Eine gültige positive Dezimalzahl mit höchstens sechs Nachkommastellen angeben"}
	}
	return number, nil
}

func fuelModuleDecimalString(number *big.Rat) string {
	text := number.FloatString(6)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "" || text == "-0" {
		return "0"
	}
	return text
}

func fuelModuleMinor(text string, positive bool) (int64, error) {
	if !fuelMinorPattern.MatchString(text) {
		return 0, Invalid{"Betrag als ganze Centzahl angeben"}
	}
	minor, err := strconv.ParseInt(text, 10, 64)
	if err != nil || minor > fuelModuleMaxMinor || (positive && minor == 0) {
		return 0, Invalid{"Betrag liegt außerhalb des zulässigen Bereichs"}
	}
	return minor, nil
}

func fuelModuleRoundedInteger(number *big.Rat) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(number.Num(), number.Denom(), remainder)
	if new(big.Int).Lsh(new(big.Int).Abs(remainder), 1).Cmp(number.Denom()) >= 0 {
		if number.Sign() >= 0 {
			quotient.Add(quotient, big.NewInt(1))
		} else {
			quotient.Sub(quotient, big.NewInt(1))
		}
	}
	return quotient
}

func fuelModulePrice(quantity, unitPrice, totalMinor string) (fuelPriceValues, []FuelModuleWarning, error) {
	result := fuelPriceValues{Quantity: quantity, UnitPrice: unitPrice, TotalMinor: totalMinor}
	warnings := []FuelModuleWarning{}
	provided := 0
	for _, text := range []string{quantity, unitPrice, totalMinor} {
		if text != "" {
			provided++
		}
	}
	if provided < 2 {
		return result, warnings, Invalid{"Mindestens zwei Werte aus Menge, Einheitspreis und Gesamtbetrag angeben"}
	}
	var quantityValue, priceValue, totalValue *big.Rat
	var err error
	if quantity != "" {
		quantityValue, err = fuelModuleDecimal(quantity, true)
		if err != nil {
			return result, warnings, err
		}
		result.Quantity = fuelModuleDecimalString(quantityValue)
	}
	if unitPrice != "" {
		priceValue, err = fuelModuleDecimal(unitPrice, true)
		if err != nil {
			return result, warnings, err
		}
		result.UnitPrice = fuelModuleDecimalString(priceValue)
	}
	if totalMinor != "" {
		minor, minorErr := fuelModuleMinor(totalMinor, true)
		if minorErr != nil {
			return result, warnings, minorErr
		}
		totalValue = big.NewRat(minor, 100)
		result.TotalMinor = strconv.FormatInt(minor, 10)
	}
	var exact *big.Rat
	switch {
	case quantityValue == nil:
		exact = new(big.Rat).Quo(totalValue, priceValue)
		result.Quantity = fuelModuleDecimalString(exact)
		result.DerivedField = "quantity"
		quantityValue, err = fuelModuleDecimal(result.Quantity, true)
	case priceValue == nil:
		exact = new(big.Rat).Quo(totalValue, quantityValue)
		result.UnitPrice = fuelModuleDecimalString(exact)
		result.DerivedField = "unit_price"
		priceValue, err = fuelModuleDecimal(result.UnitPrice, true)
	case totalValue == nil:
		exact = new(big.Rat).Mul(quantityValue, priceValue)
		exact.Mul(exact, big.NewRat(100, 1))
		minor := fuelModuleRoundedInteger(exact)
		if !minor.IsInt64() || minor.Sign() <= 0 || minor.Int64() > fuelModuleMaxMinor {
			return result, warnings, Invalid{"Berechneter Gesamtbetrag liegt außerhalb des zulässigen Bereichs"}
		}
		result.TotalMinor = minor.String()
		result.DerivedField = "total_minor"
		totalValue = new(big.Rat).SetFrac(minor, big.NewInt(100))
	}
	if err != nil {
		return result, warnings, err
	}
	// Preserve every explicitly supplied amount. The residual is shown for review,
	// including sub-cent differences; an acknowledged warning never changes facts.
	expected := new(big.Rat).Mul(quantityValue, priceValue)
	expected.Sub(totalValue, expected).Mul(expected, big.NewRat(100, 1))
	if expected.Sign() != 0 {
		result.RoundingDeltaMinor = expected.RatString()
		code := "price_rounding"
		message := "Die Berechnung wurde auf Cent beziehungsweise sechs Nachkommastellen gerundet"
		if new(big.Rat).Abs(expected).Cmp(big.NewRat(1, 1)) >= 0 {
			code = "price_mismatch"
			message = "Menge mal Einheitspreis weicht vom eingegebenen Gesamtbetrag ab; Originalwerte bleiben erhalten"
		}
		message += "; Differenz " + expected.FloatString(6) + " Cent (exakt " + expected.RatString() + ")"
		warnings = append(warnings, FuelModuleWarning{Code: code, Message: message, NextAction: "Betrag mit dem Beleg vergleichen und Abweichung ausdrücklich bestätigen"})
	}
	return result, warnings, nil
}

func fuelModuleQuantityUnit(fuelType, unit string) error {
	wanted := ""
	switch fuelType {
	case "diesel", "petrol", "lpg":
		wanted = "l"
	case "cng", "hydrogen":
		wanted = "kg"
	case "electric":
		wanted = "kWh"
	}
	if wanted == "" || unit != wanted {
		return Invalid{"Kraftstoff und Mengenart passen nicht zusammen; Liter, Kilogramm oder kWh ausdrücklich auswählen"}
	}
	return nil
}

func fuelModuleRawText(raw json.RawMessage, limit int) (string, error) {
	var text string
	if len(raw) > limit*6+2 || json.Unmarshal(raw, &text) != nil || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > limit {
		return "", Invalid{"Text ist ungültig oder überschreitet die erlaubte Länge"}
	}
	return strings.TrimSpace(text), nil
}

// fuelModuleNormalizeInput is idempotent: typed minor-unit strings remain cents
// when the reviewed normalized command is submitted again for execution.
func fuelModuleNormalizeInput(action string, input map[string]json.RawMessage) (Record, []FuelModuleWarning, error) {
	schemas := map[string][]string{
		"vehicle.archive":       {"archived"},
		"vehicle.select_series": {"odometer_series_id", "series_version"},
		"observation.save":      {"odometer_series_id", "observed_on", "reading", "notes", "acknowledged_warnings"},
		"vehicle.save":          {"name", "registration", "fuel_type", "notes", "responsible_member_id", "next_service_on", "next_inspection_on", "insurance_renewal_on", "tank_capacity", "battery_capacity"},
		"series.save":           {"started_on", "ended_on", "initial_reading", "final_reading", "reason"},
		"fillup.save":           {"odometer_series_id", "occurred_on", "fuel_type", "odometer", "quantity", "quantity_unit", "total_minor", "unit_price", "currency_code", "station", "full_tank", "previous_fill_missing", "notes", "acknowledged_warnings"},
		"fillup.delete":         {},
		"charge.save":           {"odometer_series_id", "occurred_on", "odometer", "energy_kwh", "measurement_basis", "total_minor", "currency_code", "location", "charge_kind", "soc_start", "soc_end", "notes", "acknowledged_warnings"},
		"charge.delete":         {},
		"trip.save":             {"odometer_series_id", "started_on", "ended_on", "start_odometer", "end_odometer", "purpose", "notes", "acknowledged_warnings"},
		"trip.delete":           {},
		"odometer.replace":      {"closed_on", "final_reading", "started_on", "initial_reading", "reason", "acknowledged_warnings"},
		"settings.save":         {"default_vehicle_id", "default_series_id", "default_station", "minimum_consumption", "maximum_consumption", "maximum_fill_quantity", "maximum_daily_distance", "maximum_unit_price", "plausibility_unit", "plausibility_currency_code"},
		"import.stage":          {"profile", "csv_text", "odometer_series_id", "columns", "date_format", "decimal_separator", "delimiter", "distance_unit", "quantity_unit", "fuel_type", "currency_code", "full_tank_values", "missing_values", "history_complete", "acknowledged_warnings"},
		"import.confirm":        {"job_id", "job_version", "decisions", "acknowledged_warnings"},
		"capture.stage":         {"attachment_id", "kind", "odometer_series_id", "acknowledged_warnings"},
		"capture.confirm":       {"job_id", "job_version", "values", "acknowledged_warnings"},
	}
	allowed, ok := schemas[action]
	if !ok {
		return nil, nil, Invalid{"Unbekannter Fahrzeugvorgang"}
	}
	if len(input) > len(allowed) {
		return nil, nil, Invalid{"Vorgang enthält nicht unterstützte Felder"}
	}
	output := Record{}
	for name, raw := range input {
		if name == "series_version" && action == "vehicle.select_series" {
			var version int64
			if json.Unmarshal(raw, &version) != nil || version <= 0 {
				return nil, nil, Invalid{"Version der ausgewählten Zählerreihe erneut laden"}
			}
			output[name] = version
			continue
		}
		if !slices.Contains(allowed, name) {
			return nil, nil, Invalid{"Vorgang enthält nicht unterstützte Felder"}
		}
		if name == "acknowledged_warnings" {
			codes := []string{}
			if len(raw) > 2048 || json.Unmarshal(raw, &codes) != nil || len(codes) > 20 {
				return nil, nil, Invalid{"Bestätigte Hinweise sind ungültig"}
			}
			for _, code := range codes {
				if len(code) > 80 || strings.TrimSpace(code) != code {
					return nil, nil, Invalid{"Bestätigter Hinweis ist ungültig"}
				}
			}
			slices.Sort(codes)
			output[name] = slices.Compact(codes)
			continue
		}
		if slices.Contains([]string{"columns", "decisions", "values", "full_tank_values", "missing_values"}, name) {
			if len(raw) > 256<<10 || !json.Valid(raw) {
				return nil, nil, Invalid{"Strukturierte Vorgangsdaten sind zu groß oder ungültig"}
			}
			output[name] = json.RawMessage(slices.Clone(raw))
			continue
		}
		if name == "full_tank" || name == "previous_fill_missing" || name == "archived" || name == "history_complete" {
			if len(raw) > 16 {
				return nil, nil, Invalid{"Schalter muss ausdrücklich wahr oder falsch sein"}
			}
			literal := strings.TrimSpace(string(raw))
			if literal != "true" && literal != "false" {
				return nil, nil, Invalid{"Tankstatus muss ausdrücklich wahr oder falsch sein"}
			}
			output[name] = literal == "true"
			continue
		}
		if string(raw) == "null" {
			if slices.Contains([]string{"notes", "station", "registration", "default_station", "location"}, name) {
				output[name] = ""
			} else {
				output[name] = nil
			}
			continue
		}
		limit := 300
		if name == "notes" {
			limit = 10_000
			if action == "fillup.save" || action == "vehicle.save" {
				limit = 20_000
			}
		}
		if slices.Contains([]string{"location", "purpose", "default_station"}, name) {
			limit = 200
		}
		if name == "name" {
			limit = 100
		}
		if name == "csv_text" {
			limit = 2 << 20
		}
		text, err := fuelModuleRawText(raw, limit)
		if err != nil {
			return nil, nil, err
		}
		if name == "notes" && len(text) > 20_000 {
			return nil, nil, Invalid{"Notizen überschreiten die zulässige Byteanzahl"}
		}
		if name == "csv_text" || name == "delimiter" {
			// Preserve the actual uploaded text for its provenance digest. The
			// explicit tab separator likewise must not become an empty string.
			// The bounded UTF-8/string validation above still applies.
			if err = json.Unmarshal(raw, &text); err != nil {
				return nil, nil, Invalid{"CSV-Text ist ungültig"}
			}
		}
		output[name] = text
	}
	return fuelModuleValidateValues(action, output)
}

func fuelModuleValidateValues(action string, data Record) (Record, []FuelModuleWarning, error) {
	warnings := []FuelModuleWarning{}
	required := map[string][]string{
		"vehicle.select_series": {"odometer_series_id", "series_version"},
		"observation.save":      {"odometer_series_id", "observed_on", "reading"},
		"vehicle.save":          {"name", "fuel_type"},
		"series.save":           {"started_on", "initial_reading", "reason"},
		"fillup.save":           {"odometer_series_id", "occurred_on", "fuel_type", "odometer", "quantity_unit", "currency_code"},
		"charge.save":           {"occurred_on", "energy_kwh", "measurement_basis", "currency_code", "charge_kind"},
		"trip.save":             {"odometer_series_id", "started_on", "ended_on", "start_odometer", "end_odometer", "purpose"},
		"odometer.replace":      {"closed_on", "final_reading", "started_on", "initial_reading", "reason"},
		"import.stage":          {"profile", "csv_text", "currency_code", "fuel_type"},
		"import.confirm":        {"job_id", "job_version"},
		"capture.stage":         {"attachment_id", "kind", "odometer_series_id"},
		"capture.confirm":       {"job_id", "job_version"},
	}
	for _, field := range required[action] {
		if String(data[field]) == "" {
			return nil, nil, Invalid{"Erforderliche Angabe fehlt: " + field}
		}
	}
	if text := String(data["job_version"]); text != "" {
		version, err := strconv.ParseInt(text, 10, 64)
		if err != nil || version <= 0 || strconv.FormatInt(version, 10) != text {
			return nil, nil, Invalid{"Prüfversion ist ungültig; Vorschau erneut laden"}
		}
	}
	if action == "capture.stage" && !slices.Contains([]string{"receipt", "odometer"}, String(data["kind"])) {
		return nil, nil, Invalid{"Tankbeleg oder Kilometerfoto auswählen"}
	}
	if currency := String(data["currency_code"]); currency != "" && !fuelCurrencyPattern.MatchString(currency) {
		return nil, nil, Invalid{"Währung mit drei großen ASCII-Buchstaben angeben"}
	}
	if action == "vehicle.save" && !slices.Contains([]string{"diesel", "petrol", "lpg", "cng", "hydrogen", "electric"}, String(data["fuel_type"])) {
		return nil, nil, Invalid{"Unterstützte Kraftstoff- oder Energieart auswählen"}
	}
	if action == "vehicle.archive" {
		if _, ok := data["archived"].(bool); !ok {
			return nil, nil, Invalid{"Archivieren oder Wiederherstellen ausdrücklich auswählen"}
		}
	}
	for _, field := range []string{"occurred_on", "observed_on", "started_on", "ended_on", "closed_on", "next_service_on", "next_inspection_on", "insurance_renewal_on"} {
		if text := String(data[field]); text != "" && !value.Date(text) {
			return nil, nil, Invalid{"Ungültiges Datum: " + field}
		}
	}
	for _, field := range []string{"odometer_series_id", "default_vehicle_id", "default_series_id", "attachment_id", "job_id", "responsible_member_id"} {
		if text := String(data[field]); text != "" {
			if !value.ValidUUID(text) {
				return nil, nil, Invalid{"Ungültiger Verweis: " + field}
			}
			data[field] = strings.ToLower(text)
		}
	}
	for _, field := range []string{"odometer", "reading", "initial_reading", "final_reading", "start_odometer", "end_odometer", "energy_kwh", "soc_start", "soc_end", "tank_capacity", "battery_capacity", "minimum_consumption", "maximum_consumption", "maximum_fill_quantity", "maximum_daily_distance", "maximum_unit_price"} {
		if text := String(data[field]); text != "" {
			positive := field == "energy_kwh" || field == "tank_capacity" || field == "battery_capacity" || strings.HasPrefix(field, "maximum_") || field == "minimum_consumption"
			number, err := fuelModuleDecimal(text, positive)
			if err != nil {
				return nil, nil, err
			}
			if strings.HasPrefix(field, "soc_") && number.Cmp(big.NewRat(100, 1)) > 0 {
				return nil, nil, Invalid{"Ladezustand muss zwischen null und hundert Prozent liegen"}
			}
			if strings.HasPrefix(field, "soc_") && !new(big.Rat).Mul(number, big.NewRat(100, 1)).IsInt() {
				return nil, nil, Invalid{"Ladezustand mit höchstens zwei Nachkommastellen angeben"}
			}
			data[field] = fuelModuleDecimalString(number)
		}
	}
	if action == "fillup.save" {
		for _, flag := range []string{"full_tank", "previous_fill_missing"} {
			if _, ok := data[flag].(bool); !ok {
				return nil, nil, Invalid{"Volltankstatus und fehlende Vortankung ausdrücklich angeben"}
			}
		}
		if err := fuelModuleQuantityUnit(String(data["fuel_type"]), String(data["quantity_unit"])); err != nil {
			return nil, nil, err
		}
		if String(data["fuel_type"]) == "electric" {
			return nil, nil, Invalid{"Elektrische Energie als eigenen Ladevorgang erfassen"}
		}
		price, priceWarnings, err := fuelModulePrice(String(data["quantity"]), String(data["unit_price"]), String(data["total_minor"]))
		if err != nil {
			return nil, nil, err
		}
		data["quantity"], data["unit_price"], data["total_minor"] = price.Quantity, price.UnitPrice, price.TotalMinor
		warnings = append(warnings, priceWarnings...)
	} else if text := String(data["total_minor"]); text != "" {
		minor, err := fuelModuleMinor(text, false)
		if err != nil {
			return nil, nil, err
		}
		data["total_minor"] = strconv.FormatInt(minor, 10)
	}
	for _, pair := range [][2]string{{"started_on", "ended_on"}, {"closed_on", "started_on"}} {
		first, last := String(data[pair[0]]), String(data[pair[1]])
		if first != "" && last != "" && first > last {
			return nil, nil, Invalid{"Beginn und Ende sind nicht chronologisch"}
		}
	}
	if action == "trip.save" {
		first, firstErr := fuelModuleDecimal(String(data["start_odometer"]), false)
		last, lastErr := fuelModuleDecimal(String(data["end_odometer"]), false)
		if firstErr != nil || lastErr != nil || first.Cmp(last) >= 0 || String(data["purpose"]) == "" {
			return nil, nil, Invalid{"Fahrt benötigt einen Zweck und einen höheren Endkilometerstand"}
		}
	}
	if action == "charge.save" {
		if (String(data["odometer"]) == "") != (String(data["odometer_series_id"]) == "") {
			return nil, nil, Invalid{"Kilometerstand und Zählerreihe gemeinsam angeben oder beide ausdrücklich unbekannt lassen"}
		}
		if !slices.Contains([]string{"wall", "battery", "unknown"}, String(data["measurement_basis"])) {
			return nil, nil, Invalid{"Energiemessung an Netz, Batterie oder unbekannter Messstelle ausdrücklich auswählen"}
		}
		start, end := String(data["soc_start"]), String(data["soc_end"])
		if start != "" && end != "" {
			first, _ := fuelModuleDecimal(start, false)
			last, _ := fuelModuleDecimal(end, false)
			if first.Cmp(last) > 0 {
				return nil, nil, Invalid{"Ladezustand nach dem Laden darf nicht kleiner sein"}
			}
		}
		if !slices.Contains([]string{"home", "public", "work", "other"}, String(data["charge_kind"])) {
			return nil, nil, Invalid{"Ladeart ausdrücklich auswählen"}
		}
	}
	if action == "settings.save" {
		if String(data["default_series_id"]) != "" && String(data["default_vehicle_id"]) == "" {
			return nil, nil, Invalid{"Eine persönliche Zählerreihe benötigt das zugehörige Standardfahrzeug"}
		}
		hasQuantityBounds := String(data["minimum_consumption"]) != "" || String(data["maximum_consumption"]) != "" || String(data["maximum_fill_quantity"]) != "" || String(data["maximum_unit_price"]) != ""
		if hasQuantityBounds && !slices.Contains([]string{"l", "kg", "kWh"}, String(data["plausibility_unit"])) {
			return nil, nil, Invalid{"Mengenart für Plausibilitätsgrenzen ausdrücklich auswählen"}
		}
		if String(data["maximum_unit_price"]) != "" && !fuelCurrencyPattern.MatchString(String(data["plausibility_currency_code"])) {
			return nil, nil, Invalid{"Währung für die persönliche Preisgrenze ausdrücklich auswählen"}
		}
		minimum, minErr := fuelModuleDecimal(String(data["minimum_consumption"]), false)
		maximum, maxErr := fuelModuleDecimal(String(data["maximum_consumption"]), true)
		if minErr == nil && maxErr == nil && minimum.Cmp(maximum) >= 0 {
			return nil, nil, Invalid{"Untergrenze des Verbrauchs muss unter der Obergrenze liegen"}
		}
	}
	return data, warnings, nil
}

func fuelModulePlausibility(data, previous, settings Record) []FuelModuleWarning {
	warnings := []FuelModuleWarning{}
	checks := []struct {
		field   string
		setting string
		code    string
		message string
	}{
		{field: "quantity", setting: "maximum_fill_quantity", code: "quantity_outlier", message: "Die Menge überschreitet die persönlich festgelegte Grenze"},
		{field: "unit_price", setting: "maximum_unit_price", code: "price_outlier", message: "Der Einheitspreis überschreitet die persönlich festgelegte Grenze"},
		{field: "consumption", setting: "maximum_consumption", code: "consumption_high", message: "Der berechnete Verbrauch liegt über der persönlich festgelegten Grenze"},
	}
	for _, check := range checks {
		if String(settings["plausibility_unit"]) != String(data["quantity_unit"]) || String(data["quantity_unit"]) == "" {
			continue
		}
		if check.field == "unit_price" && String(settings["plausibility_currency_code"]) != String(data["currency_code"]) {
			continue
		}
		number, numberErr := fuelModuleDecimal(String(data[check.field]), false)
		bound, boundErr := fuelModuleDecimal(String(settings[check.setting]), true)
		if numberErr == nil && boundErr == nil && number.Cmp(bound) > 0 {
			warnings = append(warnings, FuelModuleWarning{Code: check.code, Message: check.message, NextAction: "Originalwert kontrollieren und ausdrücklich bestätigen"})
		}
	}
	reading, readingErr := fuelModuleDecimal(String(data["odometer"]), false)
	prior, priorErr := fuelModuleDecimal(String(previous["odometer"]), false)
	if readingErr == nil && priorErr == nil && reading.Cmp(prior) < 0 {
		warnings = append(warnings, FuelModuleWarning{Code: "odometer_decrease", Message: "Der vorgeschlagene Stand liegt unter dem vorherigen Stand dieser Reihe", NextAction: "Stand, Datum und Zählerreihe prüfen; bei Tachowechsel eine neue Reihe beginnen"})
	}
	if readingErr == nil && priorErr == nil && reading.Cmp(prior) >= 0 {
		currentDate, currentErr := time.Parse("2006-01-02", String(data["occurred_on"]))
		priorDate, priorDateErr := time.Parse("2006-01-02", String(previous["occurred_on"]))
		bound, boundErr := fuelModuleDecimal(String(settings["maximum_daily_distance"]), true)
		if currentErr == nil && priorDateErr == nil && boundErr == nil && !currentDate.Before(priorDate) {
			days := max((currentDate.Unix()-priorDate.Unix())/86_400, 1)
			allowed := new(big.Rat).Mul(bound, big.NewRat(days, 1))
			if new(big.Rat).Sub(reading, prior).Cmp(allowed) > 0 {
				warnings = append(warnings, FuelModuleWarning{Code: "distance_outlier", Message: "Die dokumentierte Strecke überschreitet die persönliche Tagesgrenze", NextAction: "Datum, Zählerreihe und Kilometerstand kontrollieren und ausdrücklich bestätigen"})
			}
		}
	}
	consumption, consumptionErr := fuelModuleDecimal(String(data["consumption"]), true)
	minimum, minimumErr := fuelModuleDecimal(String(settings["minimum_consumption"]), false)
	if consumptionErr == nil && minimumErr == nil && String(data["quantity_unit"]) != "" &&
		String(settings["plausibility_unit"]) == String(data["quantity_unit"]) && consumption.Cmp(minimum) < 0 {
		warnings = append(warnings, FuelModuleWarning{Code: "consumption_low", Message: "Der berechnete Verbrauch liegt unter der persönlich festgelegten Grenze", NextAction: "Volltankstatus, fehlende Tankungen und Einheiten kontrollieren"})
	}
	return warnings
}

func fuelModuleChargePlausibility(data, settings Record) []FuelModuleWarning {
	comparison := Record{"quantity": data["energy_kwh"], "quantity_unit": "kWh", "currency_code": data["currency_code"]}
	warnings := fuelModulePlausibility(comparison, Record{}, settings)
	if String(settings["plausibility_unit"]) != "kWh" || String(settings["plausibility_currency_code"]) != String(data["currency_code"]) || data["total_minor"] == nil || String(data["total_minor"]) == "" {
		return warnings
	}
	energy, energyErr := fuelModuleDecimal(String(data["energy_kwh"]), true)
	minor, minorErr := fuelModuleMinor(String(data["total_minor"]), false)
	limit, limitErr := fuelModuleDecimal(String(settings["maximum_unit_price"]), true)
	if energyErr == nil && minorErr == nil && limitErr == nil {
		price := new(big.Rat).Quo(big.NewRat(minor, 100), energy)
		if price.Cmp(limit) > 0 {
			warnings = append(warnings, FuelModuleWarning{Code: "price_outlier", Message: "Die exakten Kosten je gemessener kWh überschreiten die persönliche Preisgrenze", NextAction: "Energie, Währung und Gesamtbetrag am Originalbeleg kontrollieren"})
		}
	}
	return warnings
}

func fuelModulePhysicalScope(vehicle, series, proposed Record, occurredOn string) error {
	vehicleID, seriesID := String(vehicle["id"]), String(series["id"])
	validReferences := value.ValidUUID(vehicleID) && value.ValidUUID(seriesID) && String(series["vehicle_id"]) == vehicleID
	if !validReferences || String(vehicle["archived_at"]) != "" || String(proposed["odometer_series_id"]) != seriesID {
		return Invalid{"Aktives Fahrzeug und passende Zählerreihe erneut auswählen"}
	}
	if !value.Date(occurredOn) || occurredOn < String(series["started_on"]) ||
		(String(series["ended_on"]) != "" && occurredOn > String(series["ended_on"])) {
		return Invalid{"Datum liegt außerhalb der ausdrücklich ausgewählten Zählerreihe"}
	}
	reading, readingErr := fuelModuleDecimal(String(proposed["odometer"]), false)
	initial, initialErr := fuelModuleDecimal(String(series["initial_reading"]), false)
	if readingErr != nil || initialErr != nil || reading.Cmp(initial) < 0 {
		return Invalid{"Zählerstand darf nicht unter dem Anfangsstand dieser Reihe liegen"}
	}
	if final := String(series["final_reading"]); final != "" {
		limit, err := fuelModuleDecimal(final, false)
		if err != nil || reading.Cmp(limit) > 0 {
			return Invalid{"Zählerstand liegt über dem bestätigten Abschlussstand dieser Reihe"}
		}
	}
	return nil
}
