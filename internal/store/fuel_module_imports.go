package store

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const (
	fuelImportMaxBytes    = 2 << 20
	fuelImportMaxRows     = 1000
	fuelImportSeriesError = "Zählerreihe fehlt oder ist nicht eindeutig passend; vor der Übernahme ausdrücklich auswählen"
)

func fuelModuleImportProfiles() []Record {
	return []Record{
		{"id": "fuelio", "label": "Fuelio CSV · Log", "source": "https://www.fuel.io/faq_backup_help.html", "native_log": true, "mapping_required": false, "note": "Offizielle Beispielstruktur; Datumsformat und Einheiten werden vor Übernahme angezeigt. Nur Tankprotokoll, keine Kostenabschnitte."},
		{"id": "drivvo", "label": "Drivvo CSV · explizite Zuordnung", "source": "https://www.drivvo.com/en/faq/", "native_log": false, "mapping_required": true, "note": "Offizieller Verlaufsexport; Spalten, Datum, Dezimalzeichen und Einheiten ausdrücklich zuordnen. Keine Verbindung zum Dienst."},
		{"id": "spritmonitor", "label": "Spritmonitor CSV · explizite Zuordnung", "source": "https://www.spritmonitor.de/en/faq.html", "native_log": false, "mapping_required": true, "note": "CSV-Export mit eigener Feldzuordnung; keine unbestätigte Annahme zu sprach- oder versionsabhängigen Spalten."},
		{"id": "household", "label": "Household Tankdaten CSV", "native_log": false, "mapping_required": false, "note": "ISO-Datum, km, explizite Mengenart und Währung, ganzzahlige Centbeträge; keine implizite Währungsumrechnung."},
	}
}

func fuelModuleImportObject(raw any, output any) error {
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > 256<<10 || json.Unmarshal(encoded, output) != nil {
		return Invalid{"CSV-Zuordnung oder Entscheidungen sind ungültig"}
	}
	return nil
}

func fuelModuleImportParse(ctx context.Context, profile, text string, options Record) (Record, []FuelModuleWarning, error) {
	if !slices.Contains([]string{"fuelio", "drivvo", "spritmonitor", "household"}, profile) {
		return nil, nil, Invalid{"Ein dokumentiertes CSV-Profil auswählen"}
	}
	if len(text) == 0 || len(text) > fuelImportMaxBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, nil, Invalid{"CSV muss gültiges UTF-8 sein und darf höchstens zwei MiB enthalten"}
	}
	sourceHash := sha256.Sum256([]byte(text))
	text = strings.TrimPrefix(text, "\ufeff")
	delimiter := String(options["delimiter"])
	if delimiter == "" && (profile == "fuelio" || profile == "household") {
		delimiter = ","
	}
	if delimiter != "," && delimiter != ";" && delimiter != "\t" {
		return nil, nil, Invalid{"CSV-Trennzeichen ausdrücklich als Komma, Semikolon oder Tabulator auswählen"}
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = rune(delimiter[0])
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false
	rows := []Record{}
	warnings := []FuelModuleWarning{}
	header := []string{}
	mapping := map[string]string{}
	if options["columns"] != nil {
		if err := fuelModuleImportObject(options["columns"], &mapping); err != nil {
			return nil, nil, err
		}
	}
	if (profile == "drivvo" || profile == "spritmonitor") && len(mapping) == 0 {
		return nil, nil, Invalid{"Exportspalten dieses Profils vor der Vorschau ausdrücklich zuordnen"}
	}
	if len(mapping) > 16 {
		return nil, nil, Invalid{"Zu viele CSV-Spaltenzuordnungen"}
	}
	fingerprints := map[string]int{}
	section := ""
	vehicleHeader := []string{}
	vehicleMetadata := Record{}
	metadataDate := ""
	physicalRow := 0
	recordCount := 0
	logSections := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			line := recordCount + 1
			if parse, ok := errors.AsType[*csv.ParseError](err); ok {
				line = parse.Line
			}
			return nil, nil, Invalid{fmt.Sprintf("CSV-Zeile %d ist syntaktisch ungültig", line)}
		}
		recordCount++
		if len(record) > 80 || recordCount > fuelImportMaxRows+200 {
			return nil, nil, Invalid{"CSV überschreitet die erlaubte Zeilen- oder Spaltenanzahl"}
		}
		if len(record) == 0 {
			continue
		}
		physicalRow, _ = reader.FieldPos(0)
		if profile == "fuelio" && len(record) == 1 && strings.HasPrefix(record[0], "## ") {
			section = strings.TrimSpace(record[0])
			if section == "## Log" {
				logSections++
				if logSections > 1 {
					return nil, nil, Invalid{"Mehrere Tankprotokolle müssen vor dem Import ausdrücklich getrennt werden"}
				}
			}
			continue
		}
		if profile == "fuelio" && section != "## Log" {
			if section == "## Vehicle" {
				if len(vehicleHeader) == 0 {
					vehicleHeader = slices.Clone(record)
				} else {
					for index, field := range vehicleHeader {
						if index < len(record) {
							if field == "ImportCSVDateFormat" {
								metadataDate = record[index]
							}
							if slices.Contains([]string{"Name", "DistUnit", "FuelUnit", "ImportCSVDateFormat", "TankCount"}, field) {
								vehicleMetadata[field] = record[index]
							}
						}
					}
				}
			}
			continue
		}
		if len(header) == 0 {
			header = slices.Clone(record)
			seen := map[string]bool{}
			for _, name := range header {
				if name == "" || len(name) > 120 || seen[name] {
					return nil, nil, Invalid{"CSV-Kopfzeile enthält leere, doppelte oder zu lange Spaltennamen"}
				}
				seen[name] = true
			}
			if profile == "fuelio" && len(mapping) == 0 {
				mapping = fuelModuleFuelioColumns(header)
			}
			if profile == "household" && len(mapping) == 0 {
				for _, name := range header {
					mapping[name] = name
				}
			}
			if err = fuelModuleImportMapping(mapping, header); err != nil {
				return nil, nil, err
			}
			continue
		}
		if len(rows) == fuelImportMaxRows {
			return nil, nil, Invalid{"Höchstens tausend Tankungen pro CSV-Vorschau; Datei in überprüfbare Zeiträume teilen"}
		}
		sourceIdentity, identityErr := json.Marshal([]any{"fuel-import-source-row-v1", profile, hex.EncodeToString(sourceHash[:]), physicalRow, record})
		if identityErr != nil {
			return nil, nil, identityErr
		}
		sourceFingerprint := sha256.Sum256(sourceIdentity)
		logicalRow := len(rows) + 1
		row := Record{"row_number": strconv.Itoa(logicalRow), "source_line": strconv.Itoa(physicalRow), "fingerprint": hex.EncodeToString(sourceFingerprint[:]), "decision": "pending", "errors": []string{}, "warnings": []FuelModuleWarning{}, "source_values": Record{"line": strconv.Itoa(physicalRow)}}
		if len(record) != len(header) {
			row["errors"] = []string{"Spaltenanzahl passt nicht zur Kopfzeile"}
			rows = append(rows, row)
			continue
		}
		fields := map[string]string{}
		for name, source := range mapping {
			fields[name] = strings.TrimSpace(record[slices.Index(header, source)])
		}
		evidence := Record{"line": strconv.Itoa(physicalRow), "fields": fields}
		sourceValues, sourceErr := json.Marshal(evidence)
		if sourceErr != nil {
			return nil, nil, sourceErr
		}
		if len(sourceValues) > 30<<10 {
			row["errors"] = []string{"Zugeordnete Quellwerte überschreiten 32 KiB pro Zeile"}
			rows = append(rows, row)
			continue
		}
		row["source_values"] = evidence
		rowOptions := Record{}
		for key, entry := range options {
			rowOptions[key] = entry
		}
		if profile == "fuelio" {
			if err = fuelModuleFuelioOptions(header, metadataDate, rowOptions); err != nil {
				return nil, nil, err
			}
			if tank := fields["tank_number"]; tank != "" && tank != "1" {
				row["errors"] = []string{"Mehrere Fuelio-Tanks müssen vor dem Import ausdrücklich getrennt werden"}
				rows = append(rows, row)
				continue
			}
		}
		if profile == "household" {
			rowOptions["date_format"], rowOptions["decimal_separator"], rowOptions["distance_unit"] = "iso", ".", "km"
		}
		values, rowWarnings, rowErr := fuelModuleImportValues(fields, rowOptions)
		if rowErr != nil {
			row["errors"] = []string{rowErr.Error()}
		} else {
			normalizedJSON, normalizedErr := json.Marshal(values)
			if normalizedErr != nil {
				return nil, nil, normalizedErr
			}
			if len(normalizedJSON) > 30<<10 {
				row["errors"] = []string{"Normalisierte Quellzeile überschreitet die zulässige Größe"}
				rows = append(rows, row)
				continue
			}
			key, fingerprintErr := fuelModuleImportFingerprint(values)
			if fingerprintErr != nil {
				return nil, nil, fingerprintErr
			}
			row["duplicate_fingerprint"], row["values"], row["warnings"] = key, values, rowWarnings
			if original, duplicate := fingerprints[key]; duplicate {
				row["duplicate_of_row"] = strconv.Itoa(original)
				evidence["duplicate_of_row"] = strconv.Itoa(original)
				row["decision"] = "review_duplicate"
			} else {
				fingerprints[key] = logicalRow
			}
		}
		rows = append(rows, row)
	}
	if len(header) == 0 || len(rows) == 0 {
		return nil, nil, Invalid{"Kein unterstütztes Tankprotokoll mit Datenzeilen gefunden"}
	}
	if profile == "fuelio" {
		warnings = append(warnings, FuelModuleWarning{Code: "import_log_only", Message: "Nur der Fuelio-Tankabschnitt wird übernommen; andere Sicherungsabschnitte bleiben unverändert", NextAction: "Kraftstoff, Währung und Einheiten der Vorschau bestätigen"})
	}
	return Record{"profile": profile, "source_sha256": hex.EncodeToString(sourceHash[:]), "source_metadata": vehicleMetadata, "rows": rows, "requires_confirmation": true}, warnings, nil
}

func fuelModuleImportFingerprint(values Record) (string, error) {
	// Station spelling, note changes and recalculated unit prices are not reliable
	// evidence of another physical fill-up. A matching identity is a review hint,
	// never automatic permission to insert, replace or discard an existing fact.
	identity := Record{}
	for _, field := range []string{"occurred_on", "odometer", "fuel_type", "quantity", "quantity_unit", "total_minor", "currency_code"} {
		identity[field] = values[field]
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func fuelModuleImportMapping(mapping map[string]string, header []string) error {
	allowed := []string{"occurred_on", "odometer", "quantity", "unit_price", "total", "total_minor", "station", "full_tank", "previous_fill_missing", "notes", "fuel_type", "quantity_unit", "currency_code", "record_type", "tank_number"}
	used := map[string]bool{}
	for target, source := range mapping {
		if !slices.Contains(allowed, target) || !slices.Contains(header, source) || used[source] {
			return Invalid{"CSV-Spaltenzuordnung enthält unbekannte oder mehrfach verwendete Felder"}
		}
		used[source] = true
	}
	for _, required := range []string{"occurred_on", "odometer", "quantity"} {
		if mapping[required] == "" {
			return Invalid{"CSV benötigt Datum, Zählerstand und Tankmenge als ausdrücklich zugeordnete Spalten"}
		}
	}
	if mapping["total"] == "" && mapping["total_minor"] == "" && mapping["unit_price"] == "" {
		return Invalid{"CSV benötigt einen Gesamtbetrag oder Einheitspreis"}
	}
	return nil
}

func fuelModuleFuelioColumns(header []string) map[string]string {
	mapping := map[string]string{}
	for _, column := range header {
		switch {
		case column == "Data" || column == "Date":
			mapping["occurred_on"] = column
		case strings.HasPrefix(column, "Odo ("):
			mapping["odometer"] = column
		case strings.HasPrefix(column, "Fuel ("):
			mapping["quantity"] = column
		case column == "Full":
			mapping["full_tank"] = column
		case column == "Price (optional)":
			mapping["total"] = column
		case column == "VolumePrice":
			mapping["unit_price"] = column
		case column == "Notes (optional)":
			mapping["notes"] = column
		case column == "Missed":
			mapping["previous_fill_missing"] = column
		case column == "TankNumber":
			mapping["tank_number"] = column
		}
	}
	return mapping
}

func fuelModuleFuelioOptions(header []string, metadataDate string, options Record) error {
	if String(options["date_format"]) == "" {
		switch metadataDate {
		case "yyyy-MM-dd":
			options["date_format"] = "iso"
		case "dd.MM.yyyy":
			options["date_format"] = "dmy"
		case "MM/dd/yyyy":
			options["date_format"] = "mdy-slash"
		}
	}
	if String(options["decimal_separator"]) == "" {
		options["decimal_separator"] = "."
	}
	for _, column := range header {
		if strings.HasPrefix(column, "Odo (") && strings.HasSuffix(column, ")") {
			unit := strings.TrimSuffix(strings.TrimPrefix(column, "Odo ("), ")")
			if selected := String(options["distance_unit"]); selected != "" && selected != unit {
				return Invalid{"Ausgewählte Streckeneinheit widerspricht der Fuelio-Kopfzeile"}
			}
			options["distance_unit"] = unit
		}
		if strings.HasPrefix(column, "Fuel (") && strings.HasSuffix(column, ")") {
			unit := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(column, "Fuel ("), ")"))
			canonical := ""
			switch unit {
			case "l", "liters", "litres":
				canonical = "l"
			case "us gallons":
				canonical = "us_gal"
			case "uk gallons", "imperial gallons":
				canonical = "imp_gal"
			case "kwh":
				canonical = "kWh"
			case "kg":
				canonical = unit
			}
			if canonical != "" {
				if selected := String(options["quantity_unit"]); selected != "" && selected != canonical {
					return Invalid{"Ausgewählte Mengenart widerspricht der Fuelio-Kopfzeile"}
				}
				options["quantity_unit"] = canonical
			}
		}
	}
	return nil
}

func fuelModuleImportValues(fields map[string]string, options Record) (Record, []FuelModuleWarning, error) {
	warnings := []FuelModuleWarning{}
	layouts := map[string]string{"iso": "2006-01-02", "dmy": "02.01.2006", "dmy-slash": "02/01/2006", "mdy-slash": "01/02/2006"}
	layout, ok := layouts[String(options["date_format"])]
	if !ok {
		return nil, nil, Invalid{"Datumsformat für dieses CSV-Profil ausdrücklich auswählen"}
	}
	date, err := time.Parse(layout, fields["occurred_on"])
	if err != nil || !value.Date(date.Format("2006-01-02")) {
		return nil, nil, Invalid{"Datum passt nicht zum ausgewählten Format"}
	}
	if kind := strings.ToLower(fields["record_type"]); kind != "" && !slices.Contains([]string{"fuel", "refueling", "refuelling", "betankung", "tankung"}, kind) {
		return nil, nil, Invalid{"Diese Exportzeile ist keine unterstützte Tankung"}
	}
	choose := func(field string) string {
		if fields[field] != "" {
			return fields[field]
		}
		return String(options[field])
	}
	currency := choose("currency_code")
	if !fuelCurrencyPattern.MatchString(currency) {
		return nil, nil, Invalid{"Währung ausdrücklich mit drei großen ASCII-Buchstaben angeben; keine Währungsumrechnung"}
	}
	separator := String(options["decimal_separator"])
	if separator != "." && separator != "," {
		return nil, nil, Invalid{"Dezimalzeichen ausdrücklich auswählen"}
	}
	decimal := func(text string, positive bool) (*big.Rat, error) {
		other := ","
		if separator == "," {
			other = "."
		}
		if strings.Contains(text, other) {
			return nil, Invalid{"Zahl enthält ein anderes Dezimal- oder Tausenderzeichen als ausgewählt"}
		}
		return fuelModuleDecimal(text, positive)
	}
	odometer, err := decimal(fields["odometer"], false)
	if err != nil {
		return nil, nil, err
	}
	quantity, err := decimal(fields["quantity"], true)
	if err != nil {
		return nil, nil, err
	}
	distanceUnit := String(options["distance_unit"])
	if distanceUnit == "mi" {
		odometer.Mul(odometer, big.NewRat(1_609_344, 1_000_000))
		warnings = append(warnings, FuelModuleWarning{Code: "import_distance_conversion", Message: "Meilen wurden mit dem exakten Faktor 1,609344 in Kilometer umgerechnet", NextAction: "Quelleneinheit und gerundeten Kilometerstand bestätigen"})
	} else if distanceUnit != "km" {
		return nil, nil, Invalid{"Kilometer oder internationale Meilen ausdrücklich auswählen"}
	}
	unit := choose("quantity_unit")
	factor := big.NewRat(1, 1)
	switch unit {
	case "us_gal":
		factor = big.NewRat(3_785_411_784, 1_000_000_000)
		unit = "l"
	case "imp_gal":
		factor = big.NewRat(454_609, 100_000)
		unit = "l"
	case "l", "kg", "kWh":
	default:
		return nil, nil, Invalid{"Mengenart ausdrücklich als Liter, Kilogramm, kWh oder eindeutig bezeichnete Gallone auswählen"}
	}
	if factor.Cmp(big.NewRat(1, 1)) != 0 {
		quantity.Mul(quantity, factor)
		warnings = append(warnings, FuelModuleWarning{Code: "import_quantity_conversion", Message: "Die ausdrücklich gewählte Gallonenart wurde exakt in Liter umgerechnet und auf sechs Stellen gerundet", NextAction: "Quelleneinheit und Literwert bestätigen"})
	}
	fuelType := choose("fuel_type")
	if err = fuelModuleQuantityUnit(fuelType, unit); err != nil {
		return nil, nil, err
	}
	if fuelType == "electric" {
		return nil, nil, Invalid{"Elektrische Energie separat als Ladevorgang erfassen; keine Tankverbrauchsvermischung"}
	}
	total := fields["total_minor"]
	if fields["total"] != "" {
		major, majorErr := decimal(fields["total"], true)
		if majorErr != nil {
			return nil, nil, majorErr
		}
		minor := new(big.Rat).Mul(major, big.NewRat(100, 1))
		if !minor.IsInt() {
			return nil, nil, Invalid{"CSV-Gesamtbetrag benötigt mehr als zwei Nachkommastellen"}
		}
		if total != "" && total != minor.Num().String() {
			return nil, nil, Invalid{"Centbetrag und Gesamtbetrag widersprechen einander"}
		}
		total = minor.Num().String()
	}
	unitPrice := ""
	if fields["unit_price"] != "" {
		price, priceErr := decimal(fields["unit_price"], true)
		if priceErr != nil {
			return nil, nil, priceErr
		}
		price.Quo(price, factor)
		unitPrice = fuelModuleDecimalString(price)
	}
	price, priceWarnings, err := fuelModulePrice(fuelModuleDecimalString(quantity), unitPrice, total)
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, priceWarnings...)
	flag := func(name string) (bool, error) {
		text := fields[name]
		if text == "" {
			if name == "previous_fill_missing" {
				complete, declared := options["history_complete"].(bool)
				if !declared {
					return false, Invalid{"Quelle enthält keine Angabe zu fehlenden Tankungen; Vollständigkeit ausdrücklich bestätigen oder Lücken als unbekannt markieren"}
				}
				return !complete, nil
			}
			return false, Invalid{"Volltankstatus fehlt; Status vor dem Import ausdrücklich zuordnen"}
		}
		configuration := "full_tank_values"
		if name == "previous_fill_missing" {
			configuration = "missing_values"
		}
		if options[configuration] != nil {
			choices := map[string]bool{}
			if err := fuelModuleImportObject(options[configuration], &choices); err != nil {
				return false, err
			}
			if selected, found := choices[text]; found {
				return selected, nil
			}
			return false, Invalid{"Tankstatus fehlt in der ausdrücklich gewählten Zuordnung"}
		}
		if text == "1" || strings.EqualFold(text, "true") {
			return true, nil
		}
		if text == "0" || strings.EqualFold(text, "false") {
			return false, nil
		}
		return false, Invalid{"Tankstatus enthält einen nicht ausdrücklich zugeordneten Wert"}
	}
	full, err := flag("full_tank")
	if err != nil {
		return nil, nil, err
	}
	missing, err := flag("previous_fill_missing")
	if err != nil {
		return nil, nil, err
	}
	if utf8.RuneCountInString(fields["station"]) > 300 || len(fields["notes"]) > 20_000 {
		return nil, nil, Invalid{"Tankstelle oder Notizen überschreiten die erlaubte Länge"}
	}
	return Record{
		"occurred_on": date.Format("2006-01-02"), "odometer": fuelModuleDecimalString(odometer), "fuel_type": fuelType,
		"quantity": price.Quantity, "quantity_unit": unit, "unit_price": price.UnitPrice, "total_minor": price.TotalMinor,
		"currency_code": currency, "station": fields["station"], "notes": fields["notes"], "full_tank": full, "previous_fill_missing": missing,
	}, warnings, nil
}

func fuelModuleResolveSeries(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, values Record, selected string) (string, error) {
	if selected != "" {
		series, err := fuelModuleFact(ctx, tx, id, "odometer_series", selected, command.VehicleID)
		if err != nil {
			return "", err
		}
		vehicle, err := fuelModuleFact(ctx, tx, id, "vehicles", command.VehicleID, command.VehicleID)
		if err != nil {
			return "", err
		}
		proposed := Record{"odometer_series_id": selected, "odometer": values["odometer"]}
		if err = fuelModulePhysicalScope(vehicle, series, proposed, String(values["occurred_on"])); err != nil {
			return "", err
		}
		return selected, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM fuel.odometer_series WHERE household_id=$1 AND vehicle_id=$2
 AND started_on<=$3::date AND(ended_on IS NULL OR ended_on>=$3::date) AND initial_reading<=$4::numeric
 AND(final_reading IS NULL OR final_reading>=$4::numeric) ORDER BY id LIMIT 2`, id.HouseholdID, command.VehicleID, values["occurred_on"], values["odometer"])
	if err != nil {
		return "", err
	}
	defer rows.Close()
	identifiers := []string{}
	for rows.Next() {
		var identifier string
		if err = rows.Scan(&identifier); err != nil {
			return "", err
		}
		identifiers = append(identifiers, identifier)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if len(identifiers) != 1 {
		return "", Invalid{"Keine eindeutige passende Zählerreihe; Reihe für diese Zeile ausdrücklich auswählen"}
	}
	return identifiers[0], nil
}

func fuelModuleImportDuplicate(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicleID string, values Record) (string, error) {
	var found string
	err := tx.QueryRow(ctx, `SELECT id::text FROM fuel.fillups WHERE household_id=$1 AND vehicle_id=$2
 AND occurred_on=$3::date AND odometer=$4::numeric AND fuel_type=$5 AND quantity=$6::numeric AND quantity_unit=$7
 AND total_minor=$8::bigint AND currency_code=$9 ORDER BY id LIMIT 1`, id.HouseholdID, vehicleID,
		values["occurred_on"], values["odometer"], values["fuel_type"], values["quantity"], values["quantity_unit"], values["total_minor"], values["currency_code"]).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return found, err
}

func (s *Store) fuelModulePlanImportRows(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, options, parsed Record) error {
	rows, ok := parsed["rows"].([]Record)
	if !ok || len(rows) > fuelImportMaxRows {
		return ErrConflict
	}
	for _, row := range rows {
		row["normalized"] = Record{}
		values, ok := row["values"].(Record)
		if !ok || len(values) == 0 {
			continue
		}
		series, err := fuelModuleResolveSeries(ctx, tx, id, command, values, String(options["odometer_series_id"]))
		if err != nil {
			var invalid Invalid
			if !errors.As(err, &invalid) && !errors.Is(err, ErrNotFound) {
				return err
			}
			values["odometer_series_id"] = nil
			row["errors"] = []string{fuelImportSeriesError}
			row["requires_series_selection"] = true
		} else {
			values["odometer_series_id"] = series
		}
		row["normalized"] = values
		duplicate, duplicateErr := fuelModuleImportDuplicate(ctx, tx, id, command.VehicleID, values)
		if duplicateErr != nil {
			return duplicateErr
		}
		if duplicate != "" {
			row["duplicate_of"] = duplicate
			row["decision"] = "review_duplicate"
		}
	}
	return nil
}

func fuelModuleImportPreviewRows(parsed Record) Record {
	output := Record{}
	for field, entry := range parsed {
		if field != "rows" {
			output[field] = entry
		}
	}
	rows, _ := parsed["rows"].([]Record)
	page := []Record{}
	remaining := 512 << 10
	for _, row := range rows {
		if len(page) == 100 {
			break
		}
		copyRow := Record{}
		for field, entry := range row {
			if field != "values" {
				copyRow[field] = entry
			}
		}
		encoded, err := json.Marshal(copyRow)
		if err != nil || len(encoded) > remaining {
			break
		}
		remaining -= len(encoded)
		page = append(page, copyRow)
	}
	output["rows"], output["row_count"], output["more_rows"] = page, strconv.Itoa(len(rows)), len(page) < len(rows)
	return output
}

func fuelModuleImportJob(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, jobID string) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'vehicle_id',vehicle_id,'odometer_series_id',odometer_series_id,
 'profile',profile,'source_sha256',encode(source_sha256,'hex'),'configuration',configuration,'summary',summary,
 'state',state,'version',version::text,'expires_at',expires_at) FROM fuel.import_jobs
 WHERE household_id=$1 AND id=$2 AND user_id=$3 AND membership_id=$4 AND generation=$5 AND vehicle_id=$6
 AND expires_at>transaction_timestamp() FOR UPDATE NOWAIT`, id.HouseholdID, jobID, id.UserID, id.MembershipID, command.Generation, command.VehicleID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 160<<10 {
		return nil, ErrConflict
	}
	return decode(raw)
}

func (s *Store) fuelModuleStageImport(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (Record, error) {
	parsed, _, err := fuelModuleImportParse(ctx, String(data["profile"]), String(data["csv_text"]), data)
	if err != nil {
		return nil, err
	}
	if err = s.fuelModulePlanImportRows(ctx, tx, id, command, data, parsed); err != nil {
		return nil, err
	}
	configuration := Record{}
	for field, entry := range data {
		if field != "csv_text" && field != "acknowledged_warnings" {
			configuration[field] = entry
		}
	}
	configurationJSON, err := json.Marshal(configuration)
	if err != nil || len(configurationJSON) > 60<<10 {
		return nil, Invalid{"Importkonfiguration ist zu groß"}
	}
	rows, _ := parsed["rows"].([]Record)
	summaryJSON, err := json.Marshal(Record{"row_count": strconv.Itoa(len(rows)), "source_metadata": parsed["source_metadata"], "profile": parsed["profile"]})
	if err != nil {
		return nil, err
	}
	sourceHash, err := hex.DecodeString(String(parsed["source_sha256"]))
	if err != nil || len(sourceHash) != sha256.Size {
		return nil, ErrConflict
	}
	jobID := value.UUID()
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM fuel.import_jobs WHERE household_id=$1 AND user_id=$2 AND generation=$3
 AND source_sha256=$4 AND vehicle_id=$5 FOR UPDATE NOWAIT`, id.HouseholdID, id.UserID, command.Generation, sourceHash, command.VehicleID).Scan(&existingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var raw []byte
	if existingID != "" {
		var processed bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM fuel.import_rows WHERE household_id=$1 AND job_id=$2 AND decision<>'pending')", id.HouseholdID, existingID).Scan(&processed); err != nil {
			return nil, err
		}
		if processed {
			return nil, Invalid{"Diese Datei wurde bereits geprüft oder teilweise übernommen; bestehenden Import öffnen"}
		}
		jobID = existingID
		if _, err = tx.Exec(ctx, "DELETE FROM fuel.import_rows WHERE household_id=$1 AND job_id=$2", id.HouseholdID, jobID); err != nil {
			return nil, err
		}
		err = tx.QueryRow(ctx, `UPDATE fuel.import_jobs AS j SET membership_id=$3,odometer_series_id=$4,profile=$5,configuration=$6,
 summary=$7,state='staged',version=version+1,updated_at=transaction_timestamp(),expires_at=transaction_timestamp()+interval '24 hours'
 WHERE household_id=$1 AND id=$2 RETURNING jsonb_build_object('id',id,'version',version::text,'state',state,'row_count',$8::text)`,
			id.HouseholdID, jobID, id.MembershipID, nullableFuel(data, "odometer_series_id"), data["profile"], configurationJSON, summaryJSON, strconv.Itoa(len(rows))).Scan(&raw)
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO fuel.import_jobs AS j(id,household_id,vehicle_id,odometer_series_id,user_id,membership_id,generation,profile,source_sha256,configuration,summary,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,transaction_timestamp()+interval '24 hours')
 RETURNING jsonb_build_object('id',id,'version',version::text,'state',state,'row_count',$12::text)`, jobID, id.HouseholdID, command.VehicleID,
			nullableFuel(data, "odometer_series_id"), id.UserID, id.MembershipID, command.Generation, data["profile"], sourceHash, configurationJSON, summaryJSON, strconv.Itoa(len(rows))).Scan(&raw)
	}
	if err != nil {
		return nil, err
	}
	staged := make([]Record, 0, len(rows))
	for _, row := range rows {
		normalized := row["normalized"]
		if normalized == nil {
			normalized = Record{}
		}
		staged = append(staged, Record{"row_number": row["row_number"], "source_fingerprint": "\\x" + String(row["fingerprint"]),
			"source_values": row["source_values"], "normalized": normalized, "errors": row["errors"], "warnings": row["warnings"], "duplicate_of": nullableFuel(row, "duplicate_of")})
	}
	stagedJSON, err := json.Marshal(staged)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO fuel.import_rows(job_id,household_id,row_number,source_fingerprint,source_values,normalized,errors,warnings,duplicate_of)
 SELECT $1::uuid,$2::uuid,x.row_number,x.source_fingerprint,x.source_values,x.normalized,x.errors,x.warnings,x.duplicate_of
 FROM jsonb_to_recordset($3::jsonb) x(row_number integer,source_fingerprint bytea,source_values jsonb,normalized jsonb,errors jsonb,warnings jsonb,duplicate_of uuid)`, jobID, id.HouseholdID, stagedJSON)
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

type fuelImportSelection struct {
	Decision  fuelImportDecision
	Values    Record
	Warnings  []FuelModuleWarning
	Duplicate string
}

func (s *Store) fuelModuleImportSelections(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (Record, []fuelImportSelection, error) {
	job, err := fuelModuleImportJob(ctx, tx, id, command, String(data["job_id"]))
	if err != nil {
		return nil, nil, err
	}
	if String(job["state"]) != "staged" || String(job["version"]) != String(data["job_version"]) {
		return nil, nil, ErrConflict
	}
	decisions, err := fuelModuleImportDecisions(data["decisions"])
	if err != nil {
		return nil, nil, err
	}
	selections := make([]fuelImportSelection, 0, len(decisions))
	seen := map[string]string{}
	for _, decision := range decisions {
		var fingerprint, state string
		var normalized, errorsJSON, warningsJSON []byte
		err = tx.QueryRow(ctx, `SELECT encode(source_fingerprint,'hex'),normalized,errors,warnings,decision FROM fuel.import_rows
 WHERE household_id=$1 AND job_id=$2 AND row_number=$3 FOR UPDATE NOWAIT`, id.HouseholdID, job["id"], decision.RowNumber).Scan(
			&fingerprint, &normalized, &errorsJSON, &warningsJSON, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		if state != "pending" || fingerprint != decision.Fingerprint {
			return nil, nil, ErrConflict
		}
		selection := fuelImportSelection{Decision: decision, Values: Record{}, Warnings: []FuelModuleWarning{}}
		if decision.Decision == "skip" {
			selections = append(selections, selection)
			continue
		}
		if len(normalized) > 32768 || len(errorsJSON) > 16384 || len(warningsJSON) > 16384 {
			return nil, nil, Invalid{"Gespeicherte Importzeile überschreitet die zulässige Größe"}
		}
		values, decodeErr := decode(normalized)
		if decodeErr != nil || len(values) == 0 {
			return nil, nil, Invalid{"Fehlerhafte Quellzeile kann nur übersprungen oder im ursprünglichen CSV korrigiert werden"}
		}
		rowErrors := []string{}
		if json.Unmarshal(errorsJSON, &rowErrors) != nil {
			return nil, nil, ErrConflict
		}
		if len(rowErrors) > 0 && (len(rowErrors) != 1 || rowErrors[0] != fuelImportSeriesError || String(values["odometer_series_id"]) != "") {
			return nil, nil, Invalid{"Fehlerhafte Quellwerte müssen vor einer Übernahme im CSV korrigiert werden"}
		}
		if json.Unmarshal(warningsJSON, &selection.Warnings) != nil {
			return nil, nil, ErrConflict
		}
		selected := decision.SeriesID
		if selected == "" {
			selected = String(values["odometer_series_id"])
		}
		if selected == "" {
			selected = String(job["odometer_series_id"])
		}
		series, seriesErr := fuelModuleResolveSeries(ctx, tx, id, command, values, selected)
		if seriesErr != nil {
			return nil, nil, seriesErr
		}
		values["odometer_series_id"] = series
		rawValues, rawErr := fuelModuleRawValues(values)
		if rawErr != nil {
			return nil, nil, rawErr
		}
		values, normalizationWarnings, normalizeErr := fuelModuleNormalizeInput("fillup.save", rawValues)
		if normalizeErr != nil {
			return nil, nil, normalizeErr
		}
		selection.Values = values
		selection.Warnings = append(selection.Warnings, normalizationWarnings...)
		vehicle, vehicleErr := fuelModuleFact(ctx, tx, id, "vehicles", command.VehicleID, command.VehicleID)
		if vehicleErr != nil {
			return nil, nil, vehicleErr
		}
		physical := FuelModulePreview{After: Record{}, Warnings: []FuelModuleWarning{}}
		if err = s.fuelModulePlanPhysical(ctx, tx, id, FuelModuleCommand{Action: "fillup.save", Generation: command.Generation, VehicleID: command.VehicleID, Data: rawValues}, values, vehicle, &physical); err != nil {
			return nil, nil, err
		}
		selection.Warnings = append(selection.Warnings, physical.Warnings...)
		selection.Duplicate, err = fuelModuleImportDuplicate(ctx, tx, id, command.VehicleID, values)
		if err != nil {
			return nil, nil, err
		}
		hint, hintErr := fuelModuleImportFingerprint(values)
		if hintErr != nil {
			return nil, nil, hintErr
		}
		if earlier := seen[hint]; earlier != "" || selection.Duplicate != "" {
			selection.Warnings = append(selection.Warnings, FuelModuleWarning{Code: "import_duplicate", Message: "Ähnliche Tankung ist bereits vorhanden oder zugleich ausgewählt", NextAction: "Dublettenentscheidung für diese Zeile ausdrücklich bestätigen"})
			if selection.Duplicate == "" {
				selection.Duplicate = "row:" + earlier
			}
		}
		seen[hint] = decision.RowNumber
		selections = append(selections, selection)
	}
	ordered := make([]fuelImportSelection, 0, len(selections))
	for _, selection := range selections {
		if selection.Decision.Decision == "import" {
			ordered = append(ordered, selection)
		}
	}
	slices.SortFunc(ordered, func(first, second fuelImportSelection) int {
		if series := strings.Compare(String(first.Values["odometer_series_id"]), String(second.Values["odometer_series_id"])); series != 0 {
			return series
		}
		if day := strings.Compare(String(first.Values["occurred_on"]), String(second.Values["occurred_on"])); day != 0 {
			return day
		}
		firstReading, _ := fuelModuleDecimal(String(first.Values["odometer"]), false)
		secondReading, _ := fuelModuleDecimal(String(second.Values["odometer"]), false)
		return firstReading.Cmp(secondReading)
	})
	for position := 1; position < len(ordered); position++ {
		first, second := ordered[position-1].Values, ordered[position].Values
		if first["odometer_series_id"] == second["odometer_series_id"] && String(first["occurred_on"]) < String(second["occurred_on"]) {
			previous, _ := fuelModuleDecimal(String(first["odometer"]), false)
			current, _ := fuelModuleDecimal(String(second["odometer"]), false)
			if previous.Cmp(current) > 0 {
				return nil, nil, Invalid{"Ausgewählte Importzeilen enthalten fallende Kilometerstände; Quelldaten oder Zählerreihen korrigieren"}
			}
		}
	}
	return job, selections, nil
}

func (s *Store) fuelModulePlanImportConfirm(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, preview *FuelModulePreview) error {
	job, selections, err := s.fuelModuleImportSelections(ctx, tx, id, command, data)
	if err != nil {
		return err
	}
	preview.Before = job
	rows := make([]Record, 0, len(selections))
	for _, selection := range selections {
		rows = append(rows, Record{"row_number": selection.Decision.RowNumber, "decision": selection.Decision.Decision,
			"normalized": selection.Values, "duplicate_of": selection.Duplicate, "requires_duplicate_ack": selection.Duplicate != "", "warnings": selection.Warnings})
		preview.Warnings = append(preview.Warnings, selection.Warnings...)
	}
	preview.After = Record{"job_id": job["id"], "job_version": job["version"], "rows": rows}
	preview.Effects = append(preview.Effects, Record{"kind": "reviewed_import", "selected_rows": strconv.Itoa(len(selections)), "atomic": true})
	return nil
}

func (s *Store) fuelModuleConfirmImport(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, requestID string) (Record, error) {
	job, selections, err := s.fuelModuleImportSelections(ctx, tx, id, command, data)
	if err != nil {
		return nil, err
	}
	imported, skipped := 0, 0
	for _, selection := range selections {
		var recordID any
		if selection.Decision.Decision == "import" {
			if selection.Duplicate != "" && !selection.Decision.DuplicateAck {
				return nil, Invalid{"Dublettenentscheidung für jede betroffene Importzeile ausdrücklich bestätigen"}
			}
			values := selection.Values
			values["vehicle_id"], values["source_kind"], values["source_job_id"] = command.VehicleID, "import", job["id"]
			values["source_row"], values["source_fingerprint"] = selection.Decision.RowNumber, "\\x"+selection.Decision.Fingerprint
			record, saveErr := fuelModuleSaveFact(ctx, tx, id, "fillups", "", 0, values)
			if saveErr != nil {
				return nil, saveErr
			}
			if err = s.fuelModuleProjectFact(ctx, tx, id, "fillups", record, command.Generation); err != nil {
				return nil, err
			}
			if err = Audit(ctx, tx, id, "fuel", "fillups", String(record["id"]), "record_write", requestID); err != nil {
				return nil, err
			}
			recordID = record["id"]
			imported++
		} else {
			skipped++
		}
		tag, updateErr := tx.Exec(ctx, `UPDATE fuel.import_rows SET decision=$4,record_id=$5,updated_at=transaction_timestamp()
 WHERE household_id=$1 AND job_id=$2 AND row_number=$3 AND decision='pending'`, id.HouseholdID, job["id"], selection.Decision.RowNumber, selection.Decision.Decision, recordID)
		if updateErr != nil {
			return nil, updateErr
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrConflict
		}
	}
	var raw []byte
	err = tx.QueryRow(ctx, `UPDATE fuel.import_jobs AS j SET state=CASE WHEN EXISTS(SELECT 1 FROM fuel.import_rows r
 WHERE r.household_id=j.household_id AND r.job_id=j.id AND r.decision='pending') THEN 'staged' ELSE 'complete' END,
 version=version+1,updated_at=transaction_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3::bigint
 RETURNING jsonb_build_object('id',id,'version',version::text,'state',state,'imported',$4::text,'skipped',$5::text)`,
		id.HouseholdID, job["id"], job["version"], strconv.Itoa(imported), strconv.Itoa(skipped)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}
