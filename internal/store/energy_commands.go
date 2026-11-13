package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/energy"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

var energyCommandFields = map[string][]string{
	"notification.save": {"register_id", "responsible_member_id", "reading_enabled", "anomaly_enabled", "stale_enabled", "stale_after_seconds"},
	"meter.save":        {"name", "unit_code", "location_note", "archived"},
	"register.save":     {"meter_id", "name", "measurement_kind", "unit_code", "direction", "timezone", "input_mode", "max_gap_seconds", "daily_warning", "base_load_warning", "min_history", "retention_days", "archived"},
	"device.save":       {"meter_id", "register_id", "serial_number", "installed_at", "initial_value", "rollover_value"},
	"device.replace": {"register_id", "old_mapping_id", "old_mapping_version", "changed_at", "old_final", "new_initial",
		"serial_number", "rollover_value", "gap_acknowledged", "reason"},
	"reading.save":   {"register_id", "device_id", "measured_at", "value", "kind", "interval_started_at", "rollover_confirmed", "note", "reason"},
	"tariff.save":    {"register_id", "starts_on", "ends_before", "price_minor_per_unit", "base_minor_per_day", "label", "close_previous"},
	"payment.save":   {"register_id", "paid_on", "amount_minor", "note"},
	"bill.save":      {"register_id", "occurred_on", "title", "amount_minor", "kind"},
	"bill.confirm":   {"confirmed"},
	"bill.handoff":   {"confirmed", "category_id"},
	"plan.save":      {"register_id", "anchor_on", "interval_days", "actual_only", "active"},
	"source.save":    {"register_id", "device_id", "adapter", "source_name", "measurement_kind", "unit_code", "enabled"},
	"import.preview": {"register_id", "device_id", "timezone", "unit_code", "csv", "delimiter"},
	"import.apply":   {"confirmed"},
}

func energyNormalize(in EnergyCommand) (Record, error) {
	fields, exists := energyCommandFields[in.Action]
	if slices.Contains(fields, "register_id") {
		fields = append(slices.Clone(fields), "register_version")
	}
	if !exists || len(in.Data) > len(fields) {
		return nil, Invalid{"Unbekannter Zählervorgang"}
	}
	out := Record{}
	flags := []string{"archived", "gap_acknowledged", "rollover_confirmed", "close_previous", "confirmed", "actual_only", "active", "enabled", "reading_enabled", "anomaly_enabled", "stale_enabled"}
	for name, raw := range in.Data {
		if !slices.Contains(fields, name) {
			return nil, Invalid{"Nicht unterstütztes Feld im Zählervorgang"}
		}
		if slices.Contains(flags, name) {
			var flag bool
			if string(raw) == "null" || json.Unmarshal(raw, &flag) != nil {
				return nil, Invalid{"Zählerschalter ausdrücklich wählen"}
			}
			out[name] = flag
			continue
		}
		maximum := 200
		if slices.Contains([]string{"note", "reason", "location_note"}, name) {
			maximum = 2000
		}
		if name == "csv" {
			maximum = 512 << 10
		}
		text, err := energyText(raw, maximum)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(name, "_id") && (name != "responsible_member_id" || text != "") && !value.ValidUUID(text) {
			return nil, Invalid{"Zählerreferenz erneut auswählen"}
		}
		if strings.HasSuffix(name, "_on") || name == "ends_before" {
			if text != "" && !value.Date(text) {
				return nil, Invalid{"Gültiges Kalenderdatum angeben"}
			}
		}
		if strings.HasSuffix(name, "_at") && text != "" {
			instant, err := time.Parse(time.RFC3339Nano, text)
			if err != nil || instant.Year() < 1900 || instant.Year() > 2200 || instant.Nanosecond()%1000 != 0 {
				return nil, Invalid{"Zeitpunkt mit eindeutigem UTC-Offset und höchstens Mikrosekunden angeben"}
			}
			text = instant.UTC().Format(time.RFC3339Nano)
		}
		if slices.Contains([]string{"value", "initial_value", "new_initial", "old_final", "rollover_value", "price_minor_per_unit", "base_minor_per_day", "daily_warning", "base_load_warning"}, name) && text != "" {
			if _, err := energy.Decimal(text); err != nil {
				return nil, Invalid{"Nichtnegativen exakten Dezimalwert mit höchstens sechs Nachkommastellen angeben"}
			}
		}
		if name == "amount_minor" {
			amount, err := strconv.ParseInt(text, 10, 64)
			if err != nil || amount < 1 || amount > 9000000000000000 || strconv.FormatInt(amount, 10) != text {
				return nil, Invalid{"Positiven Betrag als ganze Centzahl angeben"}
			}
		}
		out[name] = text
	}
	required := map[string][]string{
		"meter.save":     {"name", "unit_code"},
		"register.save":  {"meter_id", "name", "measurement_kind", "unit_code", "direction", "timezone", "input_mode", "max_gap_seconds", "min_history"},
		"device.save":    {"meter_id", "register_id", "serial_number", "installed_at", "initial_value"},
		"device.replace": {"register_id", "old_mapping_id", "old_mapping_version", "changed_at", "new_initial", "serial_number", "reason"},
		"reading.save":   {"register_id", "device_id", "measured_at", "value", "kind"},
		"tariff.save":    {"register_id", "starts_on", "price_minor_per_unit", "base_minor_per_day", "label"},
		"payment.save":   {"register_id", "paid_on", "amount_minor"},
		"bill.save":      {"register_id", "occurred_on", "title", "amount_minor", "kind"},
		"bill.handoff":   {"category_id"},
		"plan.save":      {"register_id", "anchor_on", "interval_days"},
		"source.save":    {"register_id", "device_id", "adapter", "source_name", "measurement_kind", "unit_code"},
		"import.preview": {"register_id", "device_id", "timezone", "unit_code", "csv", "delimiter"},
	}
	for _, field := range required[in.Action] {
		if String(out[field]) == "" {
			return nil, Invalid{"Pflichtfeld fehlt: " + field}
		}
	}
	if slices.Contains(fields, "register_id") {
		version, err := strconv.ParseInt(String(out["register_version"]), 10, 64)
		if err != nil || version < 1 || strconv.FormatInt(version, 10) != String(out["register_version"]) {
			return nil, Invalid{"Aktuellen Registerstand mit seiner Version erneut auswählen"}
		}
	}
	if slices.Contains([]string{"bill.confirm", "bill.handoff", "import.apply"}, in.Action) && (in.ID == "" || in.Version < 1 || out["confirmed"] != true) {
		return nil, Invalid{"Aktuellen Vorgang auswählen und Übernahme ausdrücklich bestätigen"}
	}
	if in.Action == "reading.save" && in.ID != "" && String(out["reason"]) == "" {
		return nil, Invalid{"Messkorrektur begründen; der ursprüngliche Wert bleibt erhalten"}
	}
	if in.Action == "reading.save" && !slices.Contains([]string{"actual", "estimated"}, String(out["kind"])) {
		return nil, Invalid{"Messung oder Schätzung ausdrücklich auswählen"}
	}
	if in.Action == "meter.save" && !slices.Contains([]string{"kWh", "m3", "l"}, String(out["unit_code"])) {
		return nil, Invalid{"Zählereinheit ausdrücklich auswählen"}
	}
	if in.Action == "register.save" || in.Action == "source.save" {
		if !energy.ValidType(String(out["measurement_kind"]), String(out["unit_code"])) {
			return nil, Invalid{"Messgröße und Einheit passen nicht zusammen; kW ist keine Energie"}
		}
	}
	if out["timezone"] != nil {
		if _, err := time.LoadLocation(String(out["timezone"])); err != nil {
			return nil, Invalid{"Gültige IANA-Zeitzone auswählen"}
		}
	}
	if in.ID == "" && in.Version != 0 || in.ID != "" && in.Version < 1 {
		return nil, Invalid{"Bestehenden Datensatz mit seiner aktuellen Version auswählen"}
	}
	return out, nil
}

func (s *Store) energyAuthorizeCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, in EnergyCommand) error {
	if in.Action == "source.save" && id.Role != "admin" {
		return ErrForbidden
	}
	if strings.HasPrefix(in.Action, "bill.") && in.ID != "" {
		var entry *string
		var boundGeneration *int64
		err := tx.QueryRow(ctx, `SELECT expense_id::text,expense_generation FROM meters.bills
 WHERE household_id=$1 AND id=$2 FOR UPDATE`, id.HouseholdID, in.ID).Scan(&entry, &boundGeneration)
		if err != nil {
			return energyError(err)
		}
		if entry != nil {
			current, err := requireModule(ctx, tx, id, "expenses")
			if err != nil {
				return err
			}
			if boundGeneration == nil || current != *boundGeneration {
				return ErrConflict
			}
			if _, err = lockFinancialPolicy(ctx, tx, id, "entries", *entry, financialEdit); err != nil {
				return err
			}
		}
	}
	if in.Action != "bill.handoff" {
		if len(in.TargetGenerations) != 0 {
			return Invalid{"Dieser Zählervorgang benötigt kein anderes Modul"}
		}
		return nil
	}
	generation, err := requireModule(ctx, tx, id, "expenses")
	if err != nil {
		return err
	}
	if generation != in.TargetGenerations["expenses"] || len(in.TargetGenerations) != 1 {
		return ErrConflict
	}
	var entry *string
	err = tx.QueryRow(ctx, `SELECT expense_id::text FROM meters.bills WHERE household_id=$1 AND id=$2 FOR UPDATE`, id.HouseholdID, in.ID).Scan(&entry)
	if err != nil {
		return energyError(err)
	}
	if entry != nil {
		_, err = lockFinancialPolicy(ctx, tx, id, "entries", *entry, financialEdit)
	}
	return err
}

func energyNullable(data Record, field string) any {
	if String(data[field]) == "" {
		return nil
	}
	return data[field]
}

func energyInteger(data Record, field string, minimum, maximum int) (int, error) {
	text := String(data[field])
	number, err := strconv.Atoi(text)
	if err != nil || number < minimum || number > maximum || strconv.Itoa(number) != text {
		return 0, Invalid{"Ganzzahliger Wert außerhalb des unterstützten Bereichs: " + field}
	}
	return number, nil
}

func (s *Store) energyApply(ctx context.Context, tx pgx.Tx, id auth.Identity, key, requestID string, in EnergyCommand, data Record) (Record, error) {
	if register := String(data["register_id"]); register != "" {
		var current bool
		err := tx.QueryRow(ctx, `SELECT true FROM meters.registers WHERE household_id=$1 AND id=$2 AND version=$3::bigint FOR SHARE`,
			id.HouseholdID, register, data["register_version"]).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, err
		}
	}
	rid := in.ID
	if rid == "" {
		rid = value.UUID()
	}
	switch in.Action {
	case "notification.save":
		return saveMeterNotificationSettings(ctx, tx, id, in, data)
	case "meter.save", "register.save", "device.save", "plan.save", "source.save":
		return s.energySaveManagement(ctx, tx, id, rid, in, data)
	case "reading.save":
		return energySaveReading(ctx, tx, id, rid, key, in.Version, data)
	case "device.replace":
		return energyReplaceDevice(ctx, tx, id, key, data)
	case "tariff.save", "payment.save", "bill.save", "bill.confirm", "bill.handoff":
		return s.energyFinancialCommand(ctx, tx, id, rid, in, data, requestID)
	case "import.preview", "import.apply":
		return energyImportCommand(ctx, tx, id, key, rid, in, data)
	default:
		return nil, ErrNotFound
	}
}

func energySaveReading(ctx context.Context, tx pgx.Tx, id auth.Identity, rid, key string, version int64, data Record) (Record, error) {
	if !slices.Contains([]string{"actual", "estimated"}, String(data["kind"])) {
		return nil, Invalid{"Messung oder Schätzung ausdrücklich auswählen"}
	}
	note := String(data["note"])
	if version > 0 {
		note = strings.TrimSpace(note + "\nKorrektur: " + String(data["reason"]))
	}
	if len(note) > 8000 {
		return nil, Invalid{"Korrekturhinweis ist zu lang"}
	}
	var out Record
	var err error
	if version == 0 {
		out, err = energyObject(ctx, tx, `INSERT INTO meters.readings AS x(id,household_id,register_id,device_id,measured_at,value,kind,note,
 interval_started_at,rollover_confirmed,source_key,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'register_id',x.register_id,'value',x.value::text,'kind',x.kind)`,
			rid, id.HouseholdID, data["register_id"], data["device_id"], data["measured_at"], data["value"], data["kind"], note,
			energyNullable(data, "interval_started_at"), data["rollover_confirmed"] == true, "manual/"+key, id.UserID)
	} else {
		out, err = energyObject(ctx, tx, `UPDATE meters.readings AS x SET measured_at=$6,value=$7,kind=$8,note=$9,
 interval_started_at=$10,rollover_confirmed=$11,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$3 AND register_id=$4 AND device_id=$5
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'register_id',x.register_id,'value',x.value::text,'kind',x.kind)`,
			id.HouseholdID, rid, version, data["register_id"], data["device_id"], data["measured_at"], data["value"], data["kind"], note,
			energyNullable(data, "interval_started_at"), data["rollover_confirmed"] == true)
	}
	if errors.Is(err, ErrNotFound) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO meters.plan_completions(household_id,plan_id,due_on,plan_version,reading_id,reading_revision,created_by)
	SELECT p.household_id,p.id,p.anchor_on+(((x.measured_at AT TIME ZONE r.timezone)::date-p.anchor_on)/p.interval_days)*p.interval_days,p.version,
 x.id,x.version,$3 FROM meters.reading_plans p JOIN meters.registers r ON r.id=p.register_id
 JOIN meters.readings x ON x.register_id=p.register_id AND x.id=$2
 WHERE p.household_id=$1 AND p.active AND (NOT p.actual_only OR x.kind='actual')
 AND (x.measured_at AT TIME ZONE r.timezone)::date>=p.anchor_on ON CONFLICT(plan_id,plan_version,due_on,reading_id,reading_revision) DO NOTHING`, id.HouseholdID, rid, id.UserID)
	return out, err
}
