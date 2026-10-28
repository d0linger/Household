package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

var (
	fuelCaptureDatePattern     = regexp.MustCompile(`\b([0-9]{4}-[0-9]{2}-[0-9]{2}|[0-9]{1,2}\.[0-9]{1,2}\.[0-9]{4})\b`)
	fuelCaptureQuantityPattern = regexp.MustCompile(`(?i)(?:^|[ \t:=])([0-9.,]+)\s*(kwh|liter|litre|litres|kg|l)\b`)
	fuelCaptureNumberPattern   = regexp.MustCompile(`[0-9.,]+`)
	fuelCaptureOdometerPattern = regexp.MustCompile(`(?i)(?:km[- ]?stand|kilometerstand|odometer|mileage)\s*[:=]?\s*([0-9.,]+)|(?:^|[ \t:=])([0-9.,]+)\s*km\b`)
)

type fuelCaptureCandidate struct {
	Value      string `json:"value"`
	Line       int    `json:"line"`
	Evidence   string `json:"evidence"`
	Rule       string `json:"rule"`
	Attachment string `json:"attachment_id"`
}

func fuelModuleCaptureValues(kind string, raw any) (Record, []FuelModuleWarning, error) {
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > 128<<10 {
		return nil, nil, Invalid{"Geprüfte Erkennungswerte sind ungültig oder zu groß"}
	}
	values := map[string]json.RawMessage{}
	if json.Unmarshal(encoded, &values) != nil || values == nil {
		return nil, nil, Invalid{"Geprüfte Erkennungswerte müssen ein Feldobjekt sein"}
	}
	if kind == "receipt" {
		return fuelModuleNormalizeInput("fillup.save", values)
	}
	if kind != "odometer" || len(values) > 3 {
		return nil, nil, Invalid{"Unbekannte Erkennungsart oder nicht unterstützte Fotofelder"}
	}
	output := Record{}
	for field, entry := range values {
		if !slices.Contains([]string{"observed_on", "reading", "notes"}, field) {
			return nil, nil, Invalid{"Kilometerfoto enthält ein nicht unterstütztes Feld"}
		}
		limit := 300
		if field == "notes" {
			limit = 10_000
		}
		text, textErr := fuelModuleRawText(entry, limit)
		if textErr != nil {
			return nil, nil, textErr
		}
		if field == "notes" && len(text) > 20_000 {
			return nil, nil, Invalid{"Fotonotizen überschreiten die zulässige Größe"}
		}
		output[field] = text
	}
	reading, readingErr := fuelModuleDecimal(String(output["reading"]), false)
	if readingErr != nil || !value.Date(String(output["observed_on"])) {
		return nil, nil, Invalid{"Kilometerfoto benötigt einen gültigen Stand und das ausdrücklich bestätigte Datum"}
	}
	output["reading"] = fuelModuleDecimalString(reading)
	return output, []FuelModuleWarning{}, nil
}

// fuelModuleCaptureSuggestions consumes only text obtained by the authenticated
// Core attachment/OCR adapter. Caller-provided OCR text is not a stage input.
func fuelModuleCaptureSuggestions(kind, text, attachment string, previous Record) (Record, []FuelModuleWarning, error) {
	if kind != "receipt" && kind != "odometer" {
		return nil, nil, Invalid{"Tankbeleg oder Kilometerfoto ausdrücklich auswählen"}
	}
	if !value.ValidUUID(attachment) || len(text) > 64<<10 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, nil, Invalid{"Lokaler Erkennungstext ist ungültig oder zu groß"}
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 1000 {
		return nil, nil, Invalid{"Zu viele Zeilen im lokalen Erkennungsergebnis"}
	}
	candidates := map[string][]fuelCaptureCandidate{}
	add := func(field, proposed string, position int, evidence, rule string) {
		if len(candidates[field]) >= 16 {
			return
		}
		for _, existing := range candidates[field] {
			if existing.Value == proposed {
				return
			}
		}
		snippet := []rune(strings.TrimSpace(evidence))
		if len(snippet) > 160 {
			snippet = snippet[:160]
		}
		candidates[field] = append(candidates[field], fuelCaptureCandidate{
			Value: proposed, Line: position + 1, Evidence: string(snippet), Rule: rule, Attachment: strings.ToLower(attachment),
		})
	}
	for position, line := range lines {
		if utf8.RuneCountInString(line) > 2000 {
			continue
		}
		for _, match := range fuelCaptureOdometerPattern.FindAllStringSubmatch(line, 16) {
			proposed := match[1]
			if proposed == "" {
				proposed = match[2]
			}
			number, err := fuelModuleDecimal(proposed, false)
			if err == nil {
				add("odometer", fuelModuleDecimalString(number), position, line, "explicit_odometer_label")
			}
		}
		if kind == "odometer" {
			// A bare display number is a candidate only when the whole trimmed line
			// is a decimal. Dates, trip labels and unexplained joined digits stay out.
			trimmed := strings.TrimSpace(line)
			if number, err := fuelModuleDecimal(trimmed, false); err == nil {
				add("odometer", fuelModuleDecimalString(number), position, line, "whole_display_number")
			}
			continue
		}
		for _, date := range fuelCaptureDatePattern.FindAllString(line, 16) {
			layout := "2006-01-02"
			if strings.Contains(date, ".") {
				layout = "2.1.2006"
			}
			parsed, err := time.Parse(layout, date)
			if err == nil && value.Date(parsed.Format("2006-01-02")) {
				add("occurred_on", parsed.Format("2006-01-02"), position, line, "calendar_date")
			}
		}
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "einzelpreis") && !strings.Contains(lower, "unit price") {
			for _, match := range fuelCaptureQuantityPattern.FindAllStringSubmatch(line, 16) {
				number, err := fuelModuleDecimal(match[1], true)
				if err != nil {
					continue
				}
				unit := strings.ToLower(match[2])
				if strings.HasPrefix(unit, "lit") {
					unit = "l"
				} else if unit == "kwh" {
					unit = "kWh"
				}
				add("quantity", fuelModuleDecimalString(number), position, line, "explicit_quantity_unit")
				add("quantity_unit", unit, position, line, "explicit_quantity_unit")
			}
		}
		totalLine := strings.Contains(lower, "gesamt") || strings.Contains(lower, "summe") || strings.Contains(lower, "betrag") || strings.Contains(lower, "total")
		if totalLine && !strings.Contains(lower, "mwst") && !strings.Contains(lower, "ust") {
			// Every plausible amount is retained; selecting the last number could
			// silently turn VAT, discounts or a card identifier into a paid amount.
			for _, proposed := range fuelCaptureNumberPattern.FindAllString(line, 16) {
				if !strings.ContainsAny(proposed, ".,") {
					continue
				}
				minor, err := value.Minor(proposed)
				if err == nil && minor > 0 {
					add("total_minor", strconv.FormatInt(minor, 10), position, line, "labelled_total_exact_cents")
				}
			}
		}
	}
	values := Record{}
	warnings := []FuelModuleWarning{{Code: "ocr_review", Message: "Lokale Texterkennung liefert Vorschläge und keine bestätigten Messwerte", NextAction: "Jedes übernommene Feld mit dem Originalbild vergleichen"}}
	fields := make([]string, 0, len(candidates))
	for field := range candidates {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	for _, field := range fields {
		matches := candidates[field]
		if len(matches) == 1 {
			values[field] = matches[0].Value
		} else {
			values[field] = nil
			warnings = append(warnings, FuelModuleWarning{Code: "ocr_ambiguous_" + field, Message: "Mehrere mögliche Werte für " + field, NextAction: "Den richtigen Wert ausdrücklich aus dem Bild übernehmen"})
		}
	}
	if kind == "odometer" {
		if _, ok := values["odometer"]; !ok {
			values["odometer"] = nil
		}
		warnings = append(warnings, fuelModulePlausibility(values, previous, Record{})...)
	} else {
		for _, required := range []string{"occurred_on", "quantity", "total_minor", "odometer"} {
			if _, ok := values[required]; !ok {
				values[required] = nil
				warnings = append(warnings, FuelModuleWarning{Code: "ocr_missing_" + required, Message: fmt.Sprintf("%s wurde nicht eindeutig erkannt", required), NextAction: "Fehlenden Wert manuell ergänzen"})
			}
		}
	}
	hash := sha256.Sum256([]byte(text))
	return Record{
		"kind": kind, "attachment_id": strings.ToLower(attachment), "text_sha256": hex.EncodeToString(hash[:]),
		"values": values, "candidates": candidates, "source": "local_ocr", "requires_confirmation": true,
	}, warnings, nil
}

func fuelModuleCaptureAttachment(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, attachmentID string) (Record, error) {
	attachment, err := fuelModuleCoreAttachment(ctx, tx, id, attachmentID)
	if err != nil {
		return nil, err
	}
	if String(attachment["module_id"]) != "fuel" || String(attachment["resource_type"]) != "vehicles" || String(attachment["resource_id"]) != command.VehicleID || Int(attachment["module_generation"]) != command.Generation {
		return nil, ErrNotFound
	}
	hash, hashErr := hex.DecodeString(String(attachment["sha256"]))
	if hashErr != nil || len(hash) != sha256.Size {
		return nil, ErrConflict
	}
	return attachment, nil
}

func fuelModuleCaptureJob(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, jobID string) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'vehicle_id',vehicle_id,'odometer_series_id',odometer_series_id,
 'attachment_id',attachment_id,'attachment_sha256',encode(attachment_sha256,'hex'),'kind',kind,'state',state,
 'suggestions',suggestions,'warnings',warnings,'error_code',error_code,'version',version::text,'record_id',record_id)
 FROM fuel.capture_jobs WHERE household_id=$1 AND id=$2 AND user_id=$3 AND membership_id=$4
 AND generation=$5 AND vehicle_id=$6 AND expires_at>transaction_timestamp() FOR UPDATE NOWAIT`,
		id.HouseholdID, jobID, id.UserID, id.MembershipID, command.Generation, command.VehicleID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 256<<10 {
		return nil, ErrConflict
	}
	return decode(raw)
}

func fuelModuleCapturePrevious(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicle, series string) (Record, error) {
	var day, reading string
	err := tx.QueryRow(ctx, `SELECT day::text,reading::text FROM (`+fuelModulePhysicalRows+`) facts WHERE series=$3
 ORDER BY day DESC,reading DESC,id DESC LIMIT 1`, id.HouseholdID, vehicle, series).Scan(&day, &reading)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Record{"occurred_on": day, "odometer": reading}, nil
}

func (s *Store) fuelModuleCaptureStageValues(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (Record, []FuelModuleWarning, string, Record, error) {
	attachment, err := fuelModuleCaptureAttachment(ctx, tx, id, command, String(data["attachment_id"]))
	if err != nil {
		return nil, nil, "", nil, err
	}
	series, err := fuelModuleFact(ctx, tx, id, "odometer_series", String(data["odometer_series_id"]), command.VehicleID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	previous, err := fuelModuleCapturePrevious(ctx, tx, id, command.VehicleID, String(series["id"]))
	if err != nil {
		return nil, nil, "", nil, err
	}
	text := String(attachment["source_text"])
	if text == "" && s.FuelOCREnabled {
		return Record{"kind": data["kind"], "attachment_id": attachment["id"], "values": Record{}, "requires_confirmation": true}, []FuelModuleWarning{}, "queued", attachment, nil
	}
	suggestions, warnings, err := fuelModuleCaptureSuggestions(String(data["kind"]), text, String(attachment["id"]), previous)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if text == "" {
		suggestions["source"] = "manual_review"
		warnings = []FuelModuleWarning{{Code: "capture_manual_review", Message: "Lokale OCR ist nicht aktiviert; es wurde keine Texterkennung ausgeführt", NextAction: "Werte am Originalbild ablesen und ausdrücklich bestätigen"}}
	}
	return suggestions, warnings, "review", attachment, nil
}

func (s *Store) fuelModulePlanCapture(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, preview *FuelModulePreview) error {
	if command.Action == "capture.stage" {
		suggestions, warnings, state, attachment, err := s.fuelModuleCaptureStageValues(ctx, tx, id, command, data)
		if err != nil {
			return err
		}
		preview.After = Record{"state": state, "kind": data["kind"], "attachment_id": attachment["id"], "suggestions": suggestions}
		preview.Warnings = append(preview.Warnings, warnings...)
		preview.Effects = append(preview.Effects, Record{"kind": "stage_capture", "business_records": "0", "local_only": true})
		return nil
	}
	job, values, warnings, err := s.fuelModuleCaptureConfirmation(ctx, tx, id, command, data)
	if err != nil {
		return err
	}
	preview.Before, preview.After = job, values
	if calculation := job["calculation"]; calculation != nil {
		preview.After["calculation"] = calculation
	}
	delete(preview.Before, "calculation")
	preview.Warnings = append(preview.Warnings, warnings...)
	entity := "fillups"
	if String(job["kind"]) == "odometer" {
		entity = "odometer_observations"
	}
	preview.Effects = append(preview.Effects, Record{"kind": "confirm_capture", "entity": entity, "attachment_id": job["attachment_id"]})
	return nil
}

func (s *Store) fuelModuleCaptureConfirmation(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (Record, Record, []FuelModuleWarning, error) {
	job, err := fuelModuleCaptureJob(ctx, tx, id, command, String(data["job_id"]))
	if err != nil {
		return nil, nil, nil, err
	}
	if String(job["state"]) != "review" || String(job["version"]) != String(data["job_version"]) || String(job["record_id"]) != "" {
		return nil, nil, nil, ErrConflict
	}
	attachment, err := fuelModuleCaptureAttachment(ctx, tx, id, command, String(job["attachment_id"]))
	if err != nil {
		return nil, nil, nil, err
	}
	if String(attachment["sha256"]) != String(job["attachment_sha256"]) {
		return nil, nil, nil, ErrConflict
	}
	values, warnings, err := fuelModuleCaptureValues(String(job["kind"]), data["values"])
	if err != nil {
		return nil, nil, nil, err
	}
	if String(job["kind"]) == "receipt" && String(values["odometer_series_id"]) != String(job["odometer_series_id"]) {
		return nil, nil, nil, Invalid{"Beleg gehört zur vorher ausdrücklich ausgewählten Zählerreihe; Erfassung bei Bedarf neu vorbereiten"}
	}
	if String(job["kind"]) == "odometer" {
		values["odometer_series_id"] = job["odometer_series_id"]
	}
	vehicle, err := fuelModuleFact(ctx, tx, id, "vehicles", command.VehicleID, command.VehicleID)
	if err != nil {
		return nil, nil, nil, err
	}
	physicalCommand := FuelModuleCommand{Action: "fillup.save", Generation: command.Generation, VehicleID: command.VehicleID}
	if String(job["kind"]) == "odometer" {
		physicalCommand.Action = "observation.save"
	}
	physicalCommand.Data, err = fuelModuleRawValues(values)
	if err != nil {
		return nil, nil, nil, err
	}
	if String(job["kind"]) == "receipt" {
		original, originalErr := json.Marshal(data["values"])
		if originalErr != nil || json.Unmarshal(original, &physicalCommand.Data) != nil {
			return nil, nil, nil, Invalid{"Geprüfte Belegwerte sind ungültig"}
		}
	}
	physical := FuelModulePreview{After: Record{}, Warnings: []FuelModuleWarning{}}
	if err = s.fuelModulePlanPhysical(ctx, tx, id, physicalCommand, values, vehicle, &physical); err != nil {
		return nil, nil, nil, err
	}
	job["current_comparison"] = physical.After["current_comparison"]
	job["calculation"] = physical.After["calculation"]
	warnings = append(warnings, physical.Warnings...)
	storedWarnings := []FuelModuleWarning{}
	if err = fuelModuleImportObject(job["warnings"], &storedWarnings); err != nil {
		return nil, nil, nil, err
	}
	for _, warning := range storedWarnings {
		if warning.Code != "odometer_decrease" {
			warnings = append(warnings, warning)
		}
	}
	return job, values, warnings, nil
}

func (s *Store) fuelModuleStageCapture(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record) (Record, error) {
	suggestions, warnings, state, attachment, err := s.fuelModuleCaptureStageValues(ctx, tx, id, command, data)
	if err != nil {
		return nil, err
	}
	suggestionsJSON, err := json.Marshal(suggestions)
	if err != nil {
		return nil, err
	}
	warningsJSON, err := json.Marshal(warnings)
	if err != nil {
		return nil, err
	}
	hash, err := hex.DecodeString(String(attachment["sha256"]))
	if err != nil || len(hash) != sha256.Size {
		return nil, ErrConflict
	}
	var raw []byte
	err = tx.QueryRow(ctx, `INSERT INTO fuel.capture_jobs(id,household_id,vehicle_id,odometer_series_id,user_id,membership_id,session_id,auth_version,
 generation,attachment_id,attachment_sha256,kind,state,suggestions,warnings,source_text,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,transaction_timestamp()+interval '24 hours')
 RETURNING jsonb_build_object('id',id,'version',version::text,'state',state,'kind',kind,'attachment_id',attachment_id)`,
		value.UUID(), id.HouseholdID, command.VehicleID, data["odometer_series_id"], id.UserID, id.MembershipID,
		id.SessionID, id.AuthVersion, command.Generation, attachment["id"], hash, data["kind"], state, suggestionsJSON, warningsJSON, String(attachment["source_text"])).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func (s *Store) fuelModuleConfirmCapture(ctx context.Context, tx pgx.Tx, id auth.Identity, command FuelModuleCommand, data Record, requestID string) (Record, error) {
	job, values, _, err := s.fuelModuleCaptureConfirmation(ctx, tx, id, command, data)
	if err != nil {
		return nil, err
	}
	entity := "fillups"
	values["vehicle_id"] = command.VehicleID
	if String(job["kind"]) == "odometer" {
		entity = "odometer_observations"
		values["source_kind"], values["capture_job_id"] = "photo", job["id"]
	} else {
		values["source_kind"], values["source_job_id"] = "receipt", job["id"]
	}
	record, err := fuelModuleSaveFact(ctx, tx, id, entity, "", 0, values)
	if err != nil {
		return nil, err
	}
	if err = s.fuelModuleProjectFact(ctx, tx, id, entity, record, command.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "fuel", entity, String(record["id"]), "record_write", requestID); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE fuel.capture_jobs SET state='complete',record_id=$4,version=version+1,updated_at=transaction_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$3::bigint AND state='review' AND record_id IS NULL`, id.HouseholdID, job["id"], job["version"], record["id"])
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrConflict
	}
	record["capture_job_id"] = job["id"]
	return record, nil
}
