package store

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const fuelModulePageSize = 100
const fuelModuleWindowDays = 366
const fuelModuleResponseBytes = 16 << 20
const fuelModuleExportRows = 50000

var fuelModuleExportSlots = make(chan struct{}, 2)

type fuelModuleExportKey struct{}

func fuelModuleFullExport(ctx context.Context) bool {
	full, _ := ctx.Value(fuelModuleExportKey{}).(bool)
	return full
}

// Fuel remains an explicitly manual, nonfinancial domain at Core26. Admitting
// future financial-derived physical records requires the central visibility
// contract; merely removing prices from this response would not authorize them.
func fuelModuleScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (FuelModuleContext, error) {
	var scope FuelModuleContext
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) ||
		!value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return scope, ErrForbidden
	}
	var revision int64
	var settingsVersion int64
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT ms.data_generation,h.timezone,h.currency_code,
 (transaction_timestamp() AT TIME ZONE h.timezone)::date::text,ms.state='enabled',coalesce(state.source_version,0),coalesce(settings.version,0)
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.id=$2 AND m.user_id=$3
 AND m.role=$4 AND m.inactive_at IS NULL JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL
 AND NOT u.must_change_password AND u.auth_version=$5 JOIN core.sessions s ON s.id=$6 AND s.user_id=u.id
 AND s.auth_version=u.auth_version AND s.revoked_at IS NULL AND s.idle_expires_at>transaction_timestamp()
 AND s.absolute_expires_at>transaction_timestamp() JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='fuel'
 LEFT JOIN fuel.household_state state ON state.household_id=h.id
 LEFT JOIN fuel.user_settings settings ON settings.household_id=h.id AND settings.user_id=u.id AND settings.generation=ms.data_generation
 WHERE h.id=$1
 AND(SELECT count(*)=1 AND bool_and(version=26 AND NOT dirty) FROM core.schema_migrations)`,
		id.HouseholdID, id.MembershipID, id.UserID, id.Role, id.AuthVersion, id.SessionID).Scan(
		&scope.Generation, &scope.Timezone, &scope.Currency, &scope.Today, &enabled, &revision, &settingsVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope, ErrForbidden
	}
	if err != nil {
		return scope, err
	}
	if !enabled || scope.Generation <= 0 || revision < 0 {
		return scope, ErrNotFound
	}
	scope.CanWrite = id.CanWrite()
	encoded, err := json.Marshal([]any{"fuel-module-scope-v1", id.HouseholdID, id.UserID, id.MembershipID,
		id.SessionID, id.Role, id.AuthVersion, scope.Generation, revision, settingsVersion, fuelSegmentAlgorithm, scope.Timezone, scope.Currency, scope.Today})
	if err != nil {
		return scope, err
	}
	hash := sha256.Sum256(encoded)
	scope.Stamp = hex.EncodeToString(hash[:])
	authority, err := json.Marshal([]any{"fuel-module-auth-v1", id.HouseholdID, id.UserID, id.MembershipID,
		id.SessionID, id.Role, id.AuthVersion, scope.Generation})
	if err != nil {
		return scope, err
	}
	authHash := sha256.Sum256(authority)
	scope.AuthScope = hex.EncodeToString(authHash[:])
	return scope, nil
}

func normalizeFuelModuleQuery(query FuelModuleQuery, today string) (FuelModuleQuery, error) {
	if query.Pane == "" {
		query.Pane = "overview"
	}
	switch query.Pane {
	case "overview", "capture", "history", "reports", "costs", "maintenance", "charging", "trips", "imports", "documents", "odometer", "settings":
	default:
		return query, ErrNotFound
	}
	query.Search = strings.TrimSpace(query.Search)
	if !utf8.ValidString(query.Search) || len(query.Search) > 300 || strings.ContainsRune(query.Search, '\x00') ||
		query.VehicleID != "" && !value.ValidUUID(query.VehicleID) || query.JobID != "" && !value.ValidUUID(query.JobID) || len(query.Cursor) > 4096 || len(query.Kind) > 40 {
		return query, Invalid{"Fahrzeugauswahl erneut prüfen"}
	}
	if query.Unit != "" && query.Unit != "l" && query.Unit != "kg" && query.Unit != "kWh" {
		return query, Invalid{"Mengenart ausdrücklich auswählen"}
	}
	if query.Pane == "reports" && query.Search != "" {
		return query, Invalid{"Kennzahlen nach Fahrzeug, Zeitraum und Mengenart filtern; Textsuche im Verlauf verwenden"}
	}
	allowedKinds := map[string][]string{
		"history":  {"diesel", "petrol", "lpg", "cng", "hydrogen"},
		"reports":  {"consumption", "costs", "energy", "diesel", "petrol", "lpg", "cng", "hydrogen"},
		"charging": {"home", "public", "work", "other"}, "costs": {"insurance", "tax", "repair", "maintenance", "parking", "toll", "other"},
		"odometer": {"manual", "photo"}, "capture": {"receipt", "odometer"},
	}
	if query.Kind != "" {
		found := false
		for _, kind := range allowedKinds[query.Pane] {
			found = found || query.Kind == kind
		}
		if !found {
			return query, Invalid{"Vorgangsart für diesen Bereich erneut auswählen"}
		}
	}
	if query.Pane == "reports" && (query.Kind == "energy" && query.Unit != "" && query.Unit != "kWh" ||
		query.Unit == "kWh" && query.Kind != "" && query.Kind != "energy" && query.Kind != "costs" && query.Kind != "consumption") {
		return query, Invalid{"Kraftstoffart und Energieeinheit passen nicht zusammen"}
	}
	day, err := time.Parse(time.DateOnly, today)
	if err != nil {
		return query, Invalid{"Haushaltsdatum erneut laden"}
	}
	if query.To == "" {
		query.To = today
	}
	if query.From == "" {
		query.From = day.AddDate(0, 0, -(fuelModuleWindowDays - 1)).Format(time.DateOnly)
	}
	from, fromErr := time.Parse(time.DateOnly, query.From)
	to, toErr := time.Parse(time.DateOnly, query.To)
	if fromErr != nil || toErr != nil || !value.Date(query.From) || !value.Date(query.To) || from.After(to) ||
		to.Sub(from) > time.Duration(fuelModuleWindowDays-1)*24*time.Hour {
		return query, Invalid{"Ein Zeitfenster mit höchstens 366 Kalendertagen auswählen"}
	}
	if (query.Pane == "reports" || query.Pane == "costs") && query.To > today {
		return query, Invalid{"Für Kennzahlen ein abgeschlossenes Datumsfenster bis heute auswählen"}
	}
	if query.Limit <= 0 {
		query.Limit = fuelModulePageSize
	}
	if query.Limit > 200 {
		return query, Invalid{"Höchstens 200 Datensätze je Seite auswählen"}
	}
	return query, nil
}

type fuelModuleCursor struct {
	Fingerprint string `json:"fingerprint"`
	Offset      int    `json:"offset"`
	Day         string `json:"day,omitempty"`
	ID          string `json:"id,omitempty"`
	Dataset     string `json:"dataset,omitempty"`
	Source      string `json:"source,omitempty"`
}

// This is an internal cursor. The HTTP adapter must authenticate the transport
// with its existing signing secret before exposing or accepting it. The Store
// still rechecks the complete current scope and filter fingerprint itself.
func fuelModulePageCursor(query FuelModuleQuery, scope FuelModuleContext) (fuelModuleCursor, error) {
	encoded, err := json.Marshal([]any{scope.Stamp, query.Pane, query.VehicleID, query.JobID, query.From, query.To,
		query.Search, query.Archived, query.Unit, query.Kind, query.Limit})
	if err != nil {
		return fuelModuleCursor{}, err
	}
	hash := sha256.Sum256(encoded)
	expected := hex.EncodeToString(hash[:])
	cursor := fuelModuleCursor{Fingerprint: expected}
	if query.Cursor == "" {
		return cursor, nil
	}
	decoder := json.NewDecoder(strings.NewReader(query.Cursor))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cursor); err != nil || cursor.Fingerprint != expected || cursor.Offset < 0 || cursor.Offset > 10000 ||
		(cursor.ID != "" && !value.ValidUUID(cursor.ID)) || (cursor.Day == "") != (cursor.ID == "") || len(cursor.Day) > 40 || len(cursor.Dataset) > 80 || len(cursor.Source) > 32 {
		return fuelModuleCursor{}, ErrConflict
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fuelModuleCursor{}, ErrConflict
	}
	return cursor, nil
}

func fuelModuleNext(cursor fuelModuleCursor, returned int, more bool) (string, error) {
	if !more {
		return "", nil
	}
	cursor.Offset += returned
	if cursor.Offset > 10000 {
		return "", Invalid{"Zeitraum für weitere Datensätze eingrenzen"}
	}
	encoded, err := json.Marshal(cursor)
	return string(encoded), err
}

func fuelModuleResponseBound(view FuelModuleView) error {
	encoded, err := json.Marshal(view)
	if err != nil {
		return err
	}
	if len(encoded) > fuelModuleResponseBytes {
		return Invalid{"Die Auswahl überschreitet 16 MiB; Zeitfenster oder Suche eingrenzen"}
	}
	return nil
}

func fuelReadRecords(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]Record, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	remaining := fuelModuleResponseBytes
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if raw == nil || len(raw) > remaining {
			return nil, Invalid{"Die Auswahl ist zu groß; Zeitraum oder Suche eingrenzen"}
		}
		remaining -= len(raw)
		record, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func fuelModuleOptions(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery) (Record, error) {
	vehicles, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',id,'name',name,'archived',archived_at IS NOT NULL,'version',version::text,'current_series_id',current_series_id)
 FROM fuel.vehicles WHERE household_id=$1 AND archived_at IS NULL ORDER BY lower(name),id LIMIT 101`, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	limited := len(vehicles) > 100
	if limited {
		vehicles = vehicles[:100]
	}
	series := []Record{}
	if query.VehicleID != "" {
		series, err = fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',id,'vehicle_id',vehicle_id,'started_on',started_on,'ended_on',ended_on,
 'initial_reading',initial_reading::text,'final_reading',final_reading::text,'distance_offset',distance_offset::text,'previous_series_id',previous_series_id,
 'current_selection',EXISTS(SELECT 1 FROM fuel.vehicles v WHERE v.household_id=fuel.odometer_series.household_id AND v.id=fuel.odometer_series.vehicle_id AND v.current_series_id=fuel.odometer_series.id),
 'reason',left(reason,200),'text_truncated',length(reason)>200,'version',version::text)
 FROM fuel.odometer_series WHERE household_id=$1 AND vehicle_id=$2 ORDER BY started_on DESC,id DESC LIMIT 101`, id.HouseholdID, query.VehicleID)
		if err != nil {
			return nil, err
		}
	}
	seriesLimited := len(series) > 100
	if seriesLimited {
		series = series[:100]
	}
	options := Record{"vehicles": vehicles, "series": series, "vehicles_limited": limited, "series_limited": seriesLimited,
		"vehicle_reference_url": "/api/v1/core/reference-options/fuel/vehicles", "series_reference_url": "/api/v1/core/reference-options/fuel/odometer_series",
		"window_from": query.From, "window_to": query.To, "calendar_day_limit": fuelModuleWindowDays, "page_limit": query.Limit}
	if query.VehicleID != "" {
		selected, readErr := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',v.id,'name',v.name,'registration',left(v.registration,80),
 'current_series_id',v.current_series_id,'archived',v.archived_at IS NOT NULL,'version',v.version::text,
 'tank_capacity',v.tank_capacity::text,'battery_capacity',v.battery_capacity::text)
 FROM fuel.vehicles v WHERE v.household_id=$1 AND v.id=$2`, id.HouseholdID, query.VehicleID)
		if readErr != nil {
			return nil, readErr
		}
		if len(selected) == 1 {
			options["selected_vehicle"] = selected[0]
		}
		current, readErr := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',s.id,'vehicle_id',s.vehicle_id,'started_on',s.started_on,'ended_on',s.ended_on,
 'initial_reading',s.initial_reading::text,'final_reading',s.final_reading::text,'distance_offset',s.distance_offset::text,
 'previous_series_id',s.previous_series_id,'version',s.version::text,'reason',left(s.reason,200),
 'applicable_today',s.started_on<=$3::date AND(s.ended_on IS NULL OR s.ended_on>=$3::date))
 FROM fuel.vehicles v JOIN fuel.odometer_series s ON s.household_id=v.household_id AND s.vehicle_id=v.id AND s.id=v.current_series_id
 WHERE v.household_id=$1 AND v.id=$2`, id.HouseholdID, query.VehicleID, scope.Today)
		if readErr != nil {
			return nil, readErr
		}
		if len(current) == 1 {
			options["selected_series"] = current[0]
		}
		progress, readErr := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('state',state.status,'source_version',state.source_version::text,
 'built_version',state.built_version::text,'algorithm_version',state.algorithm_version,
 'processed_rows',coalesce(state.checkpoint->>'rows','0'),'job_state',job.state,'attempts',job.attempts,'retry_at',job.next_attempt_at)
 FROM fuel.segment_state state LEFT JOIN fuel.segment_jobs job ON job.household_id=state.household_id AND job.vehicle_id=state.vehicle_id
 WHERE state.household_id=$1 AND state.vehicle_id=$2`, id.HouseholdID, query.VehicleID)
		if readErr != nil {
			return nil, readErr
		}
		if len(progress) == 1 {
			options["segments"] = progress[0]
		}
	}
	return options, nil
}

func fuelModuleSettings(ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('version',version::text,'default_vehicle_id',default_vehicle_id,
 'default_series_id',default_series_id,'default_station',default_station,'minimum_consumption',minimum_consumption::text,
 'maximum_consumption',maximum_consumption::text,'maximum_fill_quantity',maximum_fill_quantity::text,
 'maximum_daily_distance',maximum_daily_distance::text,'maximum_unit_price',maximum_unit_price::text,
 'plausibility_unit',plausibility_unit,'plausibility_currency_code',plausibility_currency_code,
 'default_vehicle_valid',EXISTS(SELECT 1 FROM fuel.vehicles v WHERE v.household_id=settings.household_id AND v.id=settings.default_vehicle_id AND v.archived_at IS NULL),
 'default_series_valid',EXISTS(SELECT 1 FROM fuel.odometer_series s JOIN fuel.vehicles v ON v.household_id=s.household_id AND v.id=s.vehicle_id
 WHERE s.household_id=settings.household_id AND s.id=settings.default_series_id AND s.vehicle_id=settings.default_vehicle_id
 AND v.archived_at IS NULL AND s.started_on<=(transaction_timestamp() AT TIME ZONE h.timezone)::date
 AND(s.ended_on IS NULL OR s.ended_on>=(transaction_timestamp() AT TIME ZONE h.timezone)::date)))
 FROM fuel.user_settings settings JOIN core.households h ON h.id=settings.household_id
 WHERE settings.household_id=$1 AND settings.user_id=$2 AND settings.generation=$3`, id.HouseholdID, id.UserID, generation).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func (s *Store) fuelModuleAllOptions(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery) (Record, error) {
	options, err := fuelModuleOptions(ctx, tx, id, scope, query)
	if err != nil {
		return nil, err
	}
	settings, err := fuelModuleSettings(ctx, tx, id, scope.Generation)
	if err != nil {
		return nil, err
	}
	defaults := Record{}
	for key, item := range settings {
		defaults[key] = item
	}
	if valid, _ := settings["default_vehicle_valid"].(bool); !valid {
		defaults["default_vehicle_id"] = nil
		defaults["default_series_id"] = nil
	}
	if valid, _ := settings["default_series_valid"].(bool); !valid {
		defaults["default_series_id"] = nil
	}
	options["settings"], options["defaults"], options["currency_code"] = settings, defaults, scope.Currency
	options["import_profiles"] = fuelModuleImportProfiles()
	options["fuel_types"], err = fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',code,'code',code,'label',label,'unit',unit_code,'fillup_allowed',code<>'electric')
 FROM fuel.fuel_types ORDER BY code LIMIT 32`)
	if err != nil {
		return nil, err
	}
	targets, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('module_id',module_id,'generation',data_generation)
 FROM core.module_states WHERE household_id=$1 AND state='enabled' AND module_id IN('expenses','chores','documents') ORDER BY module_id`, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	generations := map[string]int64{}
	for _, target := range targets {
		generations[String(target["module_id"])] = Int(target["generation"])
	}
	options["target_generations"] = generations
	references, err := s.fuelModuleCoreReadOptions(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"expense_categories", "expense_categories_limited", "documents", "documents_limited", "assignees", "assignees_limited", "capture_available"} {
		options[name] = references[name]
	}
	return options, nil
}

// FuelModuleWorkspace reads one pane in the same authenticated RR snapshot.
func (s *Store) FuelModuleWorkspace(ctx context.Context, id auth.Identity, query FuelModuleQuery) (FuelModuleView, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return FuelModuleView{}, err
	}
	defer fuelModuleRollback(tx)
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return FuelModuleView{}, err
	}
	query, err = normalizeFuelModuleQuery(query, scope.Today)
	if err != nil {
		return FuelModuleView{}, err
	}
	view, err := s.fuelModuleReadPane(ctx, tx, id, scope, query, query.Limit)
	if err != nil {
		return FuelModuleView{}, err
	}
	optionQuery := query
	if job, ok := view.Options["job"].(Record); ok && optionQuery.VehicleID == "" {
		optionQuery.VehicleID = String(job["vehicle_id"])
	}
	options, err := s.fuelModuleAllOptions(ctx, tx, id, scope, optionQuery)
	if err != nil {
		return FuelModuleView{}, err
	}
	for key, item := range view.Options {
		options[key] = item
	}
	view.Options = options
	if err = fuelModuleResponseBound(view); err != nil {
		return FuelModuleView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FuelModuleView{}, err
	}
	return view, nil
}

func fuelModuleRollback(tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

type fuelPaneSpec struct {
	table, day, projection, search, extra string
}

func fuelModuleSpec(pane string) (fuelPaneSpec, bool) {
	switch pane {
	case "history":
		return fuelPaneSpec{table: "fillups", day: "occurred_on", search: "station", extra: "true", projection: `jsonb_build_object('id',f.id,'vehicle_id',f.vehicle_id,'odometer_series_id',f.odometer_series_id,
 'occurred_on',f.occurred_on,'odometer',f.odometer::text,'quantity',f.quantity::text,'quantity_unit',f.quantity_unit,'fuel_type',f.fuel_type,
 'total_minor',f.total_minor::text,'currency_code',f.currency_code,'unit_price',f.unit_price::text,'station',left(f.station,200),'notes',left(f.notes,4000),
 'text_truncated',length(f.station)>200 OR length(f.notes)>4000,'full_tank',f.full_tank,'previous_fill_missing',f.previous_fill_missing,'version',f.version::text)`}, true
	case "charging":
		return fuelPaneSpec{table: "charging_sessions", day: "occurred_on", search: "location", extra: "true", projection: `jsonb_build_object('id',f.id,'vehicle_id',f.vehicle_id,'odometer_series_id',f.odometer_series_id,
 'occurred_on',f.occurred_on,'odometer',f.odometer::text,'energy_kwh',f.energy_kwh::text,'measurement_basis',f.measurement_basis,
 'total_minor',f.total_minor::text,'currency_code',f.currency_code,'location',f.location,'charge_kind',f.charge_kind,
 'soc_start',f.soc_start::text,'soc_end',f.soc_end::text,'notes',left(f.notes,4000),'text_truncated',length(f.notes)>4000,'version',f.version::text)`}, true
	case "trips":
		return fuelPaneSpec{table: "trips", day: "started_on", search: "purpose", extra: "true", projection: `jsonb_build_object('id',f.id,'vehicle_id',f.vehicle_id,'odometer_series_id',f.odometer_series_id,
 'started_on',f.started_on,'ended_on',f.ended_on,'start_odometer',f.start_odometer::text,'end_odometer',f.end_odometer::text,
 'distance',(f.end_odometer-f.start_odometer)::text,'purpose',f.purpose,'notes',left(f.notes,4000),'text_truncated',length(f.notes)>4000,'version',f.version::text)`}, true
	case "costs":
		return fuelPaneSpec{table: "vehicle_costs", day: "occurred_on", search: "title", extra: `NOT EXISTS(SELECT 1 FROM fuel.vehicle_costs correction WHERE correction.household_id=f.household_id AND correction.corrects_id=f.id)`, projection: `jsonb_build_object('id',f.id,'vehicle_id',f.vehicle_id,
 'occurred_on',f.occurred_on,'kind',f.kind,'title',f.title,'total_minor',f.total_minor::text,'currency_code',f.currency_code,
 'notes',left(f.notes,4000),'text_truncated',length(f.notes)>4000,'corrects_id',f.corrects_id,'version',f.version::text)`}, true
	case "odometer":
		return fuelPaneSpec{table: "odometer_observations", day: "observed_on", search: "notes", extra: "true", projection: `jsonb_build_object('id',f.id,'vehicle_id',f.vehicle_id,'odometer_series_id',f.odometer_series_id,
 'observed_on',f.observed_on,'reading',f.reading::text,'source_kind',f.source_kind,'capture_job_id',f.capture_job_id,'notes',left(f.notes,4000),'text_truncated',length(f.notes)>4000,'version',f.version::text)`}, true
	}
	return fuelPaneSpec{}, false
}

func fuelModuleKeyNext(cursor fuelModuleCursor, rows []Record, more bool) (string, error) {
	if !more || len(rows) == 0 {
		return "", nil
	}
	last := rows[len(rows)-1]
	cursor.Day, cursor.ID, cursor.Offset = String(last["_cursor_day"]), String(last["id"]), 0
	if !value.ValidUUID(cursor.ID) || cursor.Day == "" {
		return "", Invalid{"Verlaufsfortsetzung ist nicht eindeutig"}
	}
	encoded, err := json.Marshal(cursor)
	return string(encoded), err
}

func fuelModuleRawPage(ctx context.Context, tx pgx.Tx, id auth.Identity, query FuelModuleQuery, cursor fuelModuleCursor, limit int) ([]Record, bool, string, error) {
	spec, ok := fuelModuleSpec(query.Pane)
	if !ok {
		return nil, false, "", ErrNotFound
	}
	if cursor.Offset != 0 || cursor.Day != "" && !value.Date(cursor.Day) {
		return nil, false, "", ErrConflict
	}
	var vehicle, after, afterID any
	if query.VehicleID != "" {
		vehicle = query.VehicleID
	}
	if cursor.ID != "" {
		after, afterID = cursor.Day, cursor.ID
	}
	window := "f." + spec.day + ">=$3::date AND f." + spec.day + "<=$4::date"
	if query.Pane == "trips" {
		window = "f.ended_on>=$3::date AND f.started_on<=$4::date"
	}
	if fuelModuleFullExport(ctx) {
		size := "octet_length(f.notes)::bigint"
		if spec.table == "fillups" {
			size += "+octet_length(f.station)::bigint"
		}
		projection := strings.ReplaceAll(spec.projection, "left(f.notes,4000)", "f.notes")
		projection = strings.ReplaceAll(projection, "left(f.station,200)", "f.station")
		spec.projection = "CASE WHEN " + size + "<=1048576 THEN (" + projection + ")||jsonb_build_object('text_truncated',false) END"
	}
	unit, kind := "($9::text='')", "($10::text='')"
	switch query.Pane {
	case "history":
		unit, kind = "($9::text='' OR f.quantity_unit=$9)", "($10::text='' OR f.fuel_type=$10)"
	case "charging":
		unit, kind = "($9::text='' OR $9='kWh')", "($10::text='' OR f.charge_kind=$10)"
	case "costs":
		kind = "($10::text='' OR f.kind=$10)"
	case "odometer":
		kind = "($10::text='' OR f.source_kind=$10)"
	}
	// Table, column, search and extra predicates are compiled choices only.
	rows, err := fuelReadRecords(ctx, tx, "SELECT ("+spec.projection+")||jsonb_build_object('_cursor_day',f."+spec.day+") FROM "+
		pgx.Identifier{"fuel", spec.table}.Sanitize()+" f WHERE f.household_id=$1 AND($2::uuid IS NULL OR f.vehicle_id=$2) AND "+window+
		" AND($5='' OR strpos(lower(left(f."+spec.search+",4000)),lower($5))>0) AND($6::date IS NULL OR(f."+spec.day+",f.id)<($6::date,$7::uuid))"+
		" AND "+spec.extra+" AND "+unit+" AND "+kind+" ORDER BY f."+spec.day+" DESC,f.id DESC LIMIT $8",
		id.HouseholdID, vehicle, query.From, query.To, query.Search, after, afterID, limit+1, query.Unit, query.Kind)
	if err != nil {
		return nil, false, "", err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	next, err := fuelModuleKeyNext(cursor, rows, more)
	for _, row := range rows {
		delete(row, "_cursor_day")
	}
	return rows, more, next, err
}

func (s *Store) fuelModuleReadPane(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, limit int) (FuelModuleView, error) {
	view := FuelModuleView{Context: scope, Pane: query.Pane, Records: []Record{}, Options: Record{}, Warnings: []FuelModuleWarning{}}
	cursor, err := fuelModulePageCursor(query, scope)
	if err != nil {
		return view, err
	}
	if query.VehicleID != "" {
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'name',name,'registration',left(registration,80),
 'current_series_id',current_series_id,'archived',archived_at IS NOT NULL,'version',version::text,
 'tank_capacity',tank_capacity::text,'battery_capacity',battery_capacity::text)
 FROM fuel.vehicles WHERE household_id=$1 AND id=$2`, id.HouseholdID, query.VehicleID).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return view, ErrNotFound
		}
		if err != nil {
			return view, err
		}
		selected, decodeErr := decode(raw)
		if decodeErr != nil {
			return view, decodeErr
		}
		view.Options["selected_vehicle"] = selected
	}
	switch query.Pane {
	case "overview":
		if cursor.Offset != 0 {
			return view, ErrConflict
		}
		legacyQuery := FuelOverviewQuery{VehicleID: query.VehicleID, Search: query.Search, Archived: query.Archived}
		if cursor.ID != "" {
			legacyQuery.After, err = time.Parse(time.RFC3339Nano, cursor.Day)
			if err != nil {
				return view, ErrConflict
			}
			legacyQuery.AfterID = cursor.ID
		}
		legacyScope := FuelOverviewScope{Generation: scope.Generation, Timezone: scope.Timezone, Today: scope.Today}
		if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&legacyScope.Now); err != nil {
			return view, err
		}
		page, readErr := s.fuelOverviewRead(ctx, tx, id, legacyScope, legacyQuery)
		if readErr != nil {
			return view, readErr
		}
		for _, vehicle := range page.Vehicles {
			encoded, encodeErr := json.Marshal(vehicle)
			if encodeErr != nil {
				return view, encodeErr
			}
			record, decodeErr := decode(encoded)
			if decodeErr != nil {
				return view, decodeErr
			}
			view.Records = append(view.Records, record)
			if vehicle.ConsumptionState != "available" {
				warning := fuelReason(vehicle.ConsumptionState)
				warning.RecordID = vehicle.ID
				view.Warnings = append(view.Warnings, warning)
			}
		}
		view.More = page.More
		if view.More {
			last := page.Vehicles[len(page.Vehicles)-1]
			cursor.Day, cursor.ID = last.Created.Format(time.RFC3339Nano), last.ID
			encoded, encodeErr := json.Marshal(cursor)
			if encodeErr != nil {
				return view, encodeErr
			}
			view.Next = string(encoded)
		}
	case "reports":
		if query.VehicleID == "" {
			view.Warnings = append(view.Warnings, FuelModuleWarning{Code: "select_vehicle", Message: "Für Verbrauch und Strecken ein Fahrzeug auswählen", NextAction: "Fahrzeug und Zeitraum ausdrücklich auswählen"})
			return view, nil
		}
		report, readErr := s.fuelModuleReportPane(ctx, tx, id, scope, query, cursor, limit)
		for key, item := range view.Options {
			report.Options[key] = item
		}
		return report, readErr
	case "history", "charging", "trips", "costs", "odometer":
		view.Records, view.More, view.Next, err = fuelModuleRawPage(ctx, tx, id, query, cursor, limit)
		if err != nil {
			return view, err
		}
		if query.Pane == "costs" && query.VehicleID != "" {
			view.Totals, view.Warnings, err = fuelModuleCostTotals(ctx, tx, id, scope, query)
		}
	case "settings":
		settings, readErr := fuelModuleSettings(ctx, tx, id, scope.Generation)
		if readErr != nil {
			return view, readErr
		}
		if len(settings) > 0 {
			view.Records = []Record{settings}
		}
	case "maintenance":
		view.Records, view.More, view.Next, err = fuelModuleMaintenancePage(ctx, tx, id, scope, query, cursor, limit)
	case "documents":
		if query.VehicleID == "" {
			return view, nil
		}
		view.Records, err = fuelModuleCoreDocuments(ctx, tx, id, query.VehicleID, limit+1, cursor.Offset)
		if err != nil {
			return view, err
		}
		view.More = len(view.Records) > limit
		if view.More {
			view.Records = view.Records[:limit]
		}
		view.Next, err = fuelModuleNext(cursor, len(view.Records), view.More)
	case "imports", "capture":
		view, err = fuelModuleStagingPane(ctx, tx, id, scope, query, cursor, limit)
	}
	return view, err
}

func fuelModuleStagingPane(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, cursor fuelModuleCursor, limit int) (FuelModuleView, error) {
	view := FuelModuleView{Context: scope, Pane: query.Pane, Records: []Record{}, Options: Record{}, Warnings: []FuelModuleWarning{}}
	limit = min(limit, 100)
	table := "import_jobs"
	joins := ""
	projection := `jsonb_build_object('id',j.id,'vehicle_id',j.vehicle_id,'odometer_series_id',j.odometer_series_id,
 'profile',j.profile,'state',j.state,'version',j.version::text,'summary',j.summary,'expires_at',j.expires_at,
 'expired',j.expires_at<=transaction_timestamp(),'created_at',j.created_at)`
	if query.Pane == "capture" {
		table = "capture_jobs"
		joins = ` JOIN core.attachments a ON a.household_id=j.household_id AND a.id=j.attachment_id AND a.module_id='fuel'
 AND a.resource_type='vehicles' AND a.resource_id=j.vehicle_id AND a.state='ready' AND a.module_generation=j.generation
 AND a.sha256=j.attachment_sha256 JOIN core.module_states current_fuel ON current_fuel.household_id=j.household_id
 AND current_fuel.module_id='fuel' AND current_fuel.state='enabled' AND current_fuel.data_generation=j.generation
 JOIN fuel.vehicles actual_vehicle ON actual_vehicle.household_id=j.household_id AND actual_vehicle.id=j.vehicle_id `
		projection = `jsonb_build_object('id',j.id,'vehicle_id',j.vehicle_id,'odometer_series_id',j.odometer_series_id,
 'kind',j.kind,'state',j.state,'attachment_id',j.attachment_id,'version',j.version::text,'error_code',j.error_code,
 'warnings',j.warnings,'expires_at',j.expires_at,'expired',j.expires_at<=transaction_timestamp(),'record_id',j.record_id,'created_at',j.created_at)`
	}
	label, kindFilter := "j.profile", "($13::text='')"
	if query.Pane == "capture" {
		label, kindFilter = "j.kind", "($13::text='' OR j.kind=$13)"
	}
	if query.JobID == "" {
		if cursor.Offset != 0 {
			return view, ErrConflict
		}
		var vehicle, after, afterID any
		if query.VehicleID != "" {
			vehicle = query.VehicleID
		}
		if cursor.ID != "" {
			parsed, err := time.Parse(time.RFC3339Nano, cursor.Day)
			if err != nil {
				return view, ErrConflict
			}
			after, afterID = parsed, cursor.ID
		}
		rows, err := fuelReadRecords(ctx, tx, "SELECT "+projection+"||jsonb_build_object('_cursor_day',j.created_at) FROM "+
			pgx.Identifier{"fuel", table}.Sanitize()+` j `+joins+` WHERE j.household_id=$1 AND j.user_id=$2 AND j.membership_id=$3 AND j.generation=$4
 AND($5::uuid IS NULL OR j.vehicle_id=$5) AND j.created_at>=($6::date::timestamp AT TIME ZONE $8)
 AND j.created_at<(($7::date+1)::timestamp AT TIME ZONE $8)
 AND($9::timestamptz IS NULL OR(j.created_at,j.id)<($9::timestamptz,$10::uuid))
 AND($12='' OR strpos(lower(`+label+`),lower($12))>0) AND `+kindFilter+`
 ORDER BY j.created_at DESC,j.id DESC LIMIT $11`, id.HouseholdID, id.UserID, id.MembershipID, scope.Generation,
			vehicle, query.From, query.To, scope.Timezone, after, afterID, limit+1, query.Search, query.Kind)
		if err != nil {
			return view, err
		}
		view.More = len(rows) > limit
		if view.More {
			rows = rows[:limit]
		}
		view.Next, err = fuelModuleKeyNext(cursor, rows, view.More)
		for _, row := range rows {
			delete(row, "_cursor_day")
		}
		view.Records = rows
		view.Options["pagination"] = "current_authorized_job_keyset"
		return view, err
	}
	rows, err := fuelReadRecords(ctx, tx, "SELECT "+projection+" FROM "+pgx.Identifier{"fuel", table}.Sanitize()+
		" j "+joins+" WHERE j.household_id=$1 AND j.user_id=$2 AND j.membership_id=$3 AND j.generation=$4 AND j.id=$5", id.HouseholdID, id.UserID, id.MembershipID, scope.Generation, query.JobID)
	if err != nil {
		return view, err
	}
	if len(rows) != 1 || query.VehicleID != "" && String(rows[0]["vehicle_id"]) != query.VehicleID {
		return view, ErrNotFound
	}
	job := rows[0]
	job["effective_state"] = job["state"]
	view.Options["job"] = job
	if expired, _ := job["expired"].(bool); expired {
		job["effective_state"] = "expired"
		view.Warnings = append(view.Warnings, FuelModuleWarning{Code: "staging_expired", Message: "Der Vorschlag ist abgelaufen", NextAction: "Quelldatei erneut bereitstellen; es werden keine alten Vorschläge automatisch übernommen"})
		return view, nil
	}
	version := String(job["version"])
	if cursor.Dataset != "" && cursor.Dataset != version {
		return view, ErrConflict
	}
	cursor.Dataset = version
	if query.Pane == "capture" {
		if _, err = fuelModuleCoreAttachment(ctx, tx, id, String(job["attachment_id"])); err != nil {
			return view, err
		}
		var proposal []byte
		err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(suggestions::text)<=65536 THEN suggestions END
 FROM fuel.capture_jobs WHERE household_id=$1 AND id=$2 AND user_id=$3 AND membership_id=$4 AND generation=$5`,
			id.HouseholdID, query.JobID, id.UserID, id.MembershipID, scope.Generation).Scan(&proposal)
		if err != nil {
			return view, err
		}
		if proposal == nil {
			return view, Invalid{"OCR-Vorschlag überschreitet die unterstützte Größe"}
		}
		values, decodeErr := decode(proposal)
		if decodeErr != nil {
			return view, decodeErr
		}
		job["proposal"] = values
		view.Records = []Record{job}
		return view, nil
	}
	if cursor.ID != "" || cursor.Offset > 1000 {
		return view, ErrConflict
	}
	view.Records, err = fuelReadRecords(ctx, tx, `SELECT CASE WHEN octet_length(source_values::text)+octet_length(normalized::text)+octet_length(errors::text)+octet_length(warnings::text)<=131072
 THEN jsonb_build_object('row_number',row_number::text,'fingerprint',encode(source_fingerprint,'hex'),'values',normalized,
 'source_values',source_values,'errors',errors,'warnings',warnings,'decision',decision,'record_id',record_id,
 'duplicate_of',duplicate_of,'existing_duplicate',duplicate_of IS NOT NULL) END
 FROM fuel.import_rows WHERE household_id=$1 AND job_id=$2 AND row_number>$3 ORDER BY row_number LIMIT $4`, id.HouseholdID, query.JobID, cursor.Offset, limit+1)
	if err != nil {
		return view, err
	}
	view.More = len(view.Records) > limit
	if view.More {
		view.Records = view.Records[:limit]
		position, parseErr := strconv.Atoi(String(view.Records[len(view.Records)-1]["row_number"]))
		if parseErr != nil || position <= cursor.Offset || position > 1000 {
			return view, ErrConflict
		}
		cursor.Offset = position
		encoded, encodeErr := json.Marshal(cursor)
		if encodeErr != nil {
			return view, encodeErr
		}
		view.Next = string(encoded)
	}
	for _, row := range view.Records {
		if values, ok := row["values"].(map[string]any); ok {
			row["requires_series_selection"] = len(values) > 0 && String(values["odometer_series_id"]) == ""
		}
		if source, ok := row["source_values"].(map[string]any); ok {
			row["duplicate_of_row"] = source["duplicate_of_row"]
		}
	}
	return view, nil
}

func fuelCurrentReading(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicle, today string) (Record, error) {
	var selected *string
	err := tx.QueryRow(ctx, "SELECT current_series_id::text FROM fuel.vehicles WHERE household_id=$1 AND id=$2", id.HouseholdID, vehicle).Scan(&selected)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	seriesRows, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',s.id,'started_on',s.started_on,'ended_on',s.ended_on,'initial_reading',s.initial_reading::text)
 FROM fuel.odometer_series s WHERE s.household_id=$1 AND s.vehicle_id=$2 AND($3::uuid IS NULL OR s.id=$3)
 AND s.started_on<=$4::date AND(s.ended_on IS NULL OR s.ended_on>=$4::date) ORDER BY s.id LIMIT 2`, id.HouseholdID, vehicle, selected, today)
	if err != nil {
		return nil, err
	}
	if len(seriesRows) != 1 {
		reason := "no_current_series"
		if len(seriesRows) > 1 {
			reason = "overlapping_series"
		} else if selected != nil {
			reason = "selected_series_unavailable"
		}
		return Record{"reading": nil, "reason": reason}, nil
	}
	series := seriesRows[0]
	points, err := fuelReadRecords(ctx, tx, `WITH points AS(
 (SELECT id,occurred_on AS day,odometer AS reading,'fillup' AS source FROM fuel.fillups
 WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3 AND occurred_on<=$4::date ORDER BY occurred_on DESC,odometer DESC,id DESC LIMIT 2)
 UNION ALL(SELECT id,observed_on,reading,'observation' FROM fuel.odometer_observations
 WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3 AND observed_on<=$4::date ORDER BY observed_on DESC,reading DESC,id DESC LIMIT 2)
 UNION ALL(SELECT id,occurred_on,odometer,'charging' FROM fuel.charging_sessions WHERE household_id=$1 AND vehicle_id=$2
 AND odometer_series_id=$3 AND odometer IS NOT NULL AND occurred_on<=$4::date ORDER BY occurred_on DESC,odometer DESC,id DESC LIMIT 2)
 UNION ALL(SELECT id,ended_on,end_odometer,'trip' FROM fuel.trips WHERE household_id=$1 AND vehicle_id=$2
 AND odometer_series_id=$3 AND ended_on<=$4::date ORDER BY ended_on DESC,end_odometer DESC,id DESC LIMIT 2))
 SELECT jsonb_build_object('id',id,'day',day,'reading',reading::text,'source',source) FROM points ORDER BY day DESC,reading DESC,id DESC LIMIT 2`,
		id.HouseholdID, vehicle, series["id"], today)
	if err != nil {
		return nil, err
	}
	result := Record{"series_id": series["id"], "reading": series["initial_reading"], "day": series["started_on"], "source": "initial_reading",
		"reason": "known", "explicit_series": selected != nil}
	if len(points) == 0 {
		return result, nil
	}
	reading, readingErr := value.Decimal(String(points[0]["reading"]))
	initial, initialErr := value.Decimal(String(series["initial_reading"]))
	if readingErr != nil || initialErr != nil || reading.Cmp(initial) < 0 || String(points[0]["day"]) < String(series["started_on"]) ||
		series["ended_on"] != nil && String(points[0]["day"]) > String(series["ended_on"]) {
		return Record{"series_id": series["id"], "reading": nil, "reason": "reading_outside_series"}, nil
	}
	result["reading"], result["day"], result["source"], result["source_id"] = points[0]["reading"], points[0]["day"], points[0]["source"], points[0]["id"]
	var inconsistent bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM fuel.fillups WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3 AND occurred_on<$4::date AND odometer>$5::numeric
 UNION ALL SELECT 1 FROM fuel.odometer_observations WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3 AND observed_on<$4::date AND reading>$5::numeric
 UNION ALL SELECT 1 FROM fuel.charging_sessions WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3 AND occurred_on<$4::date AND odometer>$5::numeric
 UNION ALL SELECT 1 FROM fuel.trips WHERE household_id=$1 AND vehicle_id=$2 AND odometer_series_id=$3 AND ended_on<$4::date AND end_odometer>$5::numeric)`,
		id.HouseholdID, vehicle, series["id"], points[0]["day"], points[0]["reading"]).Scan(&inconsistent)
	if err != nil {
		return nil, err
	}
	if inconsistent {
		return Record{"series_id": series["id"], "reading": nil, "reason": "odometer_backwards"}, nil
	}
	if len(points) > 1 {
		prior, priorErr := value.Decimal(String(points[1]["reading"]))
		if priorErr != nil || prior.Cmp(reading) > 0 {
			return Record{"series_id": series["id"], "reading": nil, "reason": "odometer_backwards"}, nil
		}
		if String(points[0]["day"]) == String(points[1]["day"]) && prior.Cmp(reading) == 0 {
			result["source"], result["source_id"] = "multiple_documented_points", nil
		}
	}
	return result, nil
}

func fuelModuleMaintenancePage(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, cursor fuelModuleCursor, limit int) ([]Record, bool, string, error) {
	if cursor.ID != "" {
		return nil, false, "", ErrConflict
	}
	var vehicle any
	if query.VehicleID != "" {
		vehicle = query.VehicleID
	}
	notes, truncated := "left(p.notes,4000)", "length(p.notes)>4000"
	if fuelModuleFullExport(ctx) {
		notes, truncated = "p.notes", "false"
	}
	rows, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('id',p.id,'vehicle_id',p.vehicle_id,'title',p.title,'notes',`+notes+`,
 'text_truncated',`+truncated+`,'due_on',p.due_on,'due_odometer',p.due_odometer::text,'odometer_series_id',p.odometer_series_id,
 'repeat_days',p.repeat_days,'repeat_distance',p.repeat_distance::text,'active',p.active,'cycle',p.cycle::text,'task_opt_in',p.task_opt_in,
 'assignee_id',p.assignee_id,'version',p.version::text,'last_completed_on',(SELECT max(e.completed_on) FROM fuel.maintenance_events e WHERE e.household_id=p.household_id AND e.plan_id=p.id AND e.completed_on<=$7::date),
 'task_binding_id',(SELECT b.id
 FROM fuel.integration_bindings b WHERE b.household_id=p.household_id AND b.source_entity='maintenance_plans' AND b.source_id=p.id
 AND b.source_cycle=p.cycle AND b.target_module='chores' AND b.relation='maintenance_task' ORDER BY b.created_at DESC LIMIT 1))
 FROM fuel.maintenance_plans p WHERE p.household_id=$1 AND($2::uuid IS NULL OR p.vehicle_id=$2)
 AND($3='' OR strpos(lower(p.title),lower($3))>0) AND($4 OR p.active)
 ORDER BY p.active DESC,p.due_on NULLS LAST,p.id LIMIT $5 OFFSET $6`, id.HouseholdID, vehicle, query.Search, query.Archived, limit+1, cursor.Offset, scope.Today)
	if err != nil {
		return nil, false, "", err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	readings := map[string]Record{}
	for _, row := range rows {
		if bindingID := String(row["task_binding_id"]); bindingID != "" {
			binding, targetErr := fuelModuleCoreBindingView(ctx, tx, id, bindingID)
			if errors.Is(targetErr, ErrNotFound) || errors.Is(targetErr, ErrForbidden) {
				row["task_binding"] = Record{"id": bindingID, "state": "target_unavailable"}
			} else if targetErr != nil {
				return nil, false, "", targetErr
			} else {
				row["task_binding"] = binding
			}
		}
		delete(row, "task_binding_id")
		vehicleID := String(row["vehicle_id"])
		reading := readings[vehicleID]
		if reading == nil {
			reading, err = fuelCurrentReading(ctx, tx, id, vehicleID, scope.Today)
			if err != nil {
				return nil, false, "", err
			}
			readings[vehicleID] = reading
		}
		row["current_reading"] = reading
		dateState, distanceState := "unknown", "unknown"
		if day := String(row["due_on"]); day != "" {
			dateState = "upcoming"
			if day < scope.Today {
				dateState = "overdue"
			} else if day == scope.Today {
				dateState = "today"
			}
		}
		if threshold, parseErr := value.Decimal(String(row["due_odometer"])); parseErr == nil {
			actual, actualErr := value.Decimal(String(reading["reading"]))
			if actualErr == nil && String(reading["series_id"]) == String(row["odometer_series_id"]) {
				distanceState = "upcoming"
				if actual.Cmp(threshold) >= 0 {
					distanceState = "due"
				}
			} else {
				distanceState = "unknown_series_or_reading"
			}
		}
		row["date_state"], row["distance_state"] = dateState, distanceState
		row["due"] = dateState == "overdue" || dateState == "today" || distanceState == "due"
	}
	next, err := fuelModuleNext(cursor, len(rows), more)
	return rows, more, next, err
}

// FuelModuleRecord supplies complete bounded edit facts. A shortened list row
// must never become an edit payload that accidentally overwrites unseen notes.
func (s *Store) FuelModuleRecord(ctx context.Context, id auth.Identity, entity, recordID string) (FuelModuleDetail, error) {
	var detail FuelModuleDetail
	if !value.ValidUUID(recordID) {
		return detail, ErrNotFound
	}
	textSize, exact := "", ""
	switch entity {
	case "vehicles":
		textSize = "octet_length(f.name)::bigint+octet_length(f.registration)::bigint+octet_length(f.notes)::bigint"
		exact = "'tank_capacity',f.tank_capacity::text,'battery_capacity',f.battery_capacity::text"
	case "odometer_series":
		textSize = "octet_length(f.reason)::bigint"
		exact = "'initial_reading',f.initial_reading::text,'final_reading',f.final_reading::text,'distance_offset',f.distance_offset::text"
	case "fillups":
		textSize = "octet_length(f.station)::bigint+octet_length(f.notes)::bigint"
		exact = "'odometer',f.odometer::text,'quantity',f.quantity::text,'unit_price',f.unit_price::text,'total_minor',f.total_minor::text"
	case "charging_sessions":
		textSize = "octet_length(f.location)::bigint+octet_length(f.notes)::bigint"
		exact = "'odometer',f.odometer::text,'energy_kwh',f.energy_kwh::text,'total_minor',f.total_minor::text,'soc_start',f.soc_start::text,'soc_end',f.soc_end::text"
	case "trips":
		textSize = "octet_length(f.purpose)::bigint+octet_length(f.notes)::bigint"
		exact = "'start_odometer',f.start_odometer::text,'end_odometer',f.end_odometer::text"
	case "vehicle_costs":
		textSize = "octet_length(f.title)::bigint+octet_length(f.notes)::bigint"
		exact = "'total_minor',f.total_minor::text"
	case "maintenance_plans":
		textSize = "octet_length(f.title)::bigint+octet_length(f.notes)::bigint"
		exact = "'due_odometer',f.due_odometer::text,'repeat_distance',f.repeat_distance::text,'cycle',f.cycle::text"
	case "maintenance_events":
		textSize = "octet_length(f.notes)::bigint"
		exact = "'odometer',f.odometer::text,'cycle',f.cycle::text"
	case "odometer_observations":
		textSize, exact = "octet_length(f.notes)::bigint", "'reading',f.reading::text"
	default:
		return detail, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return detail, err
	}
	defer fuelModuleRollback(tx)
	detail.Context, err = fuelModuleScope(ctx, tx, id)
	if err != nil {
		return detail, err
	}
	var raw []byte
	// Entity and expression choices are closed above. Check source text before
	// materializing JSON, then preserve exact numeric strings for the browser.
	err = tx.QueryRow(ctx, "SELECT CASE WHEN "+textSize+"<=1048576 THEN (to_jsonb(f)-ARRAY['household_id','created_by','source_fingerprint'])||"+
		"jsonb_build_object('version',f.version::text,"+exact+") END FROM "+pgx.Identifier{"fuel", entity}.Sanitize()+
		" f WHERE f.household_id=$1 AND f.id=$2", id.HouseholdID, recordID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return detail, ErrNotFound
	}
	if err != nil {
		return detail, err
	}
	if raw == nil || len(raw) > 2<<20 {
		return detail, Invalid{"Datensatz ist für eine vollständige Bearbeitung zu groß; Betreiber-Sicherung verwenden"}
	}
	detail.Record, err = decode(raw)
	if err != nil {
		return detail, err
	}
	if err = tx.Commit(ctx); err != nil {
		return detail, err
	}
	return detail, nil
}

type fuelCSVLimit struct{ bytes int }

func (limit *fuelCSVLimit) Write(data []byte) (int, error) {
	if len(data) > fuelModuleResponseBytes-limit.bytes {
		return 0, Invalid{"Export überschreitet 16 MiB; ein kleineres Fenster auswählen"}
	}
	limit.bytes += len(data)
	return len(data), nil
}

// FuelModuleExport completes the exact selected scope before returning any
// rows. The HTTP adapter buffers its CSV and applies spreadsheet-safe text
// escaping; a failed bound or changed authority never returns a partial export.
func (s *Store) FuelModuleExport(ctx context.Context, id auth.Identity, query FuelModuleQuery) ([]string, [][]string, error) {
	if query.Cursor != "" {
		return nil, nil, Invalid{"Export mit einer vollständigen Auswahl statt einer Folgeseite starten"}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, fuelModuleExportKey{}, true)
	select {
	case fuelModuleExportSlots <- struct{}{}:
		defer func() { <-fuelModuleExportSlots }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	defer fuelModuleRollback(tx)
	scope, err := fuelModuleScope(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	query, err = normalizeFuelModuleQuery(query, scope.Today)
	if err != nil {
		return nil, nil, err
	}
	if query.Pane == "reports" && query.VehicleID == "" {
		return nil, nil, Invalid{"Für einen vollständigen Kennzahlenexport ein Fahrzeug auswählen"}
	}
	query.Limit = 200
	records := []Record{}
	columns := map[string]bool{}
	remaining := fuelModuleResponseBytes
	for page := 0; page <= 500; page++ {
		view, readErr := s.fuelModuleReadPane(ctx, tx, id, scope, query, query.Limit)
		if readErr != nil {
			return nil, nil, readErr
		}
		if query.Pane == "reports" && view.Totals == nil {
			return nil, nil, Invalid{"Kennzahlen sind noch nicht vollständig verfügbar; keine Teilwerte exportiert"}
		}
		for _, record := range view.Records {
			if len(records) >= fuelModuleExportRows {
				return nil, nil, Invalid{"Export umfasst mehr als 50.000 Zeilen; Zeitraum oder Suche eingrenzen"}
			}
			encoded, encodeErr := json.Marshal(record)
			if encodeErr != nil {
				return nil, nil, encodeErr
			}
			if len(encoded) > remaining {
				return nil, nil, Invalid{"Export überschreitet 16 MiB; ein kleineres Fenster auswählen"}
			}
			remaining -= len(encoded)
			for key := range record {
				if strings.HasPrefix(key, "_") {
					return nil, nil, Invalid{"Interne Fortsetzungsdaten dürfen nicht exportiert werden"}
				}
				columns[key] = true
			}
			records = append(records, record)
		}
		if !view.More {
			break
		}
		if page == 500 || view.Next == "" || view.Next == query.Cursor {
			return nil, nil, Invalid{"Exportfortsetzung überschreitet die sichere Grenze"}
		}
		query.Cursor = view.Next
	}
	if len(columns) == 0 {
		columns["id"], columns["vehicle_id"] = true, true
	}
	if len(columns) > 128 {
		return nil, nil, Invalid{"Export enthält zu viele unterschiedliche Felder"}
	}
	header := make([]string, 0, len(columns))
	for name := range columns {
		header = append(header, name)
	}
	sort.Strings(header)
	rows := make([][]string, 0, len(records))
	writer := csv.NewWriter(&fuelCSVLimit{})
	if err = writer.Write(header); err != nil {
		return nil, nil, err
	}
	for _, record := range records {
		row := make([]string, len(header))
		for column, key := range header {
			item := record[key]
			switch typed := item.(type) {
			case nil:
				row[column] = ""
			case string:
				row[column] = typed
			case bool:
				row[column] = strconv.FormatBool(typed)
			case json.Number:
				row[column] = typed.String()
			case int, int64:
				row[column] = String(item)
			default:
				encoded, encodeErr := json.Marshal(item)
				if encodeErr != nil {
					return nil, nil, encodeErr
				}
				row[column] = string(encoded)
			}
		}
		if err = writer.Write(row); err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return header, rows, nil
}

func fuelReason(code string) FuelModuleWarning {
	text := map[string][2]string{
		"no_fillups":                  {"Noch keine tatsächliche Tankung im gewählten Bereich.", "Tankung mit passender Zählerreihe dokumentieren."},
		"no_full_anchor":              {"Eine tatsächliche Volltankung als Anfang fehlt.", "Erste Volltankung dokumentieren; Anfangsstände ersetzen sie nicht."},
		"needs_next_full":             {"Der Abschnitt ist noch nicht durch eine nächste Volltankung abgeschlossen.", "Nächste tatsächliche Volltankung dokumentieren."},
		"missing_fill":                {"Eine fehlende Tankung unterbricht die Mengenbilanz.", "Fehlenden Beleg ergänzen oder einen neuen vollständigen Abschnitt abwarten."},
		"fuel_changed":                {"Ein Kraftstoffwechsel unterbricht diesen Verbrauchsabschnitt.", "Zwei Volltankungen derselben Kraftstoffart dokumentieren."},
		"zero_distance":               {"Zwischen den Grenzen ist keine positive Strecke dokumentiert.", "Kilometerstände und Zählerreihe prüfen."},
		"ambiguous_order":             {"Tag und Kilometerstand mehrerer Tankungen sind gleich; die Reihenfolge ist unbekannt.", "Doppelte oder falsch datierte Belege prüfen."},
		"unsupported_unit":            {"Die gespeicherte Mengenart passt nicht zum Kraftstoff.", "Liter, Kilogramm und elektrische Energie getrennt erfassen."},
		"odometer_backwards":          {"Der Kilometerstand fällt innerhalb derselben Zählerreihe.", "Werte korrigieren oder den geführten Tachowechsel verwenden."},
		"segments_pending":            {"Die große Historie wird abschnittsweise neu berechnet; eine unvollständige Kennzahl wird nicht gezeigt.", "Nach abgeschlossener Neuberechnung erneut laden oder ein kleineres Zeitfenster wählen."},
		"segments_stale":              {"Die Verbrauchsprojektion gehört zu einem älteren Datenstand.", "Neuberechnung abwarten; Originalbelege bleiben im Verlauf erreichbar."},
		"history_window_limit":        {"Das Fenster einschließlich nötiger Randbelege umfasst mehr als 10.000 Tankungen.", "Kleineres Zeitfenster wählen oder die begrenzte Segmentneuberechnung abschließen lassen."},
		"no_documented_distance":      {"Für diesen Kostenumfang fehlt eine eindeutig dokumentierte Strecke.", "Vollständige, nicht überlappende Zählerabschnitte oder Fahrten dokumentieren."},
		"overlapping_distance":        {"Dokumentierte Strecken überschneiden sich; ein gemeinsamer Nenner wäre mehrdeutig.", "Zählerreihen und Fahrten prüfen."},
		"unknown_cost_currency":       {"Für mindestens einen Beleg fehlt ein eindeutig bekannter Betrag oder seine Währung.", "Originalbeleg prüfen; eine heutige Haushaltswährung wird nicht rückwirkend angenommen."},
		"segments_failed":             {"Die abgeleitete Neuberechnung konnte nicht abgeschlossen werden.", "Originalbelege und Zählerreihen prüfen und einen begrenzten Neuaufbau anfordern."},
		"series_limit":                {"Mehr als 1.000 passende Zählerreihen überschreiten dieses Lesefenster.", "Fahrzeug, Serienzeiträume oder Datumsfenster eingrenzen."},
		"no_current_series":           {"Keine Zählerreihe passt zum heutigen Haushaltsdatum.", "Eine gültige Zählerreihe hinterlegen oder ihren Zeitraum prüfen."},
		"overlapping_series":          {"Mehrere Zählerreihen passen gleichzeitig; ihr aktueller Stand ist nicht eindeutig.", "Reihen ausdrücklich auswählen oder den geführten Tachowechsel verwenden."},
		"selected_series_unavailable": {"Die ausdrücklich ausgewählte Reihe ist heute nicht anwendbar.", "Ausgewählte Reihe und Beginn beziehungsweise Ende prüfen."},
		"reading_outside_series":      {"Ein dokumentierter Stand passt nicht zu Anfang oder Zeitraum der Reihe.", "Originalbeleg und Zählerreihe korrigieren; ein unbekannter Stand wird nicht als Null gezeigt."},
		"invalid_history":             {"Die gespeicherte Historie ist nicht konsistent auswertbar.", "Tankdaten und zugehörige Zählerreihen prüfen."},
		"no_completed_interval":       {"Im gewählten Fenster ist kein eindeutig abgeschlossener Volltankabschnitt verfügbar.", "Volltankstatus, fehlende Belege und Zählerreihe prüfen oder ein anderes Zeitfenster wählen."},
	}
	pair, known := text[code]
	if !known {
		pair = [2]string{"Für diese Angabe fehlt eine eindeutige Grundlage.", "Originaldaten und Auswahl prüfen."}
	}
	return FuelModuleWarning{Code: code, Message: pair[0], NextAction: pair[1]}
}
