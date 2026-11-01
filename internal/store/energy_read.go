package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/civiltime"
	"household/internal/value"
)

func (s *Store) EnergyOverview(ctx context.Context, id auth.Identity, search string) (Record, error) {
	if len(search) > 200 {
		return nil, Invalid{"Suchtext ist zu lang"}
	}
	scope, err := s.energyRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	meters, err := energyRows(scope.ctx, scope.tx, 250, `SELECT to_jsonb(m) FROM meters.meters m
 WHERE household_id=$1 AND strpos(lower(name),lower($2))>0 ORDER BY name,id LIMIT 251`, id.HouseholdID, search)
	if err != nil {
		return nil, err
	}
	registers, err := energyRows(scope.ctx, scope.tx, 250, `SELECT jsonb_build_object('id',r.id,'version',r.version,'meter_id',r.meter_id,
 'name',r.name,'measurement_kind',r.measurement_kind,'unit_code',r.unit_code,'direction',r.direction,'timezone',r.timezone,
 'input_mode',r.input_mode,'is_default',r.is_default,'archived_at',r.archived_at,'data_revision',r.data_revision,
 'max_gap_seconds',r.max_gap_seconds,'min_history',r.min_history,'retention_days',r.retention_days,
 'daily_warning',r.daily_warning::text,'base_load_warning',r.base_load_warning::text,
 'last_value',last.value::text,'last_measured_at',last.measured_at,'last_quality',last.kind,
 'last_consumption',segment.quantity::text,'last_from',segment.from_at,'last_through',segment.through_at,'last_interval_quality',segment.quality,
 'recalculation_pending',EXISTS(SELECT 1 FROM meters.dirty_ranges q WHERE q.register_id=r.id),
 'active_device_count',active.total,'active_device_id',CASE WHEN active.total=1 THEN active.device END)
 FROM meters.registers r JOIN meters.meters m ON m.id=r.meter_id
 LEFT JOIN LATERAL(SELECT measured_at,value,kind FROM (
  SELECT measured_at,value,kind FROM meters.readings WHERE register_id=r.id AND r.input_mode='manual'
  UNION ALL SELECT measured_at,value,kind FROM meters.telemetry WHERE register_id=r.id AND r.input_mode='telemetry') observed
  WHERE measured_at<=transaction_timestamp() ORDER BY measured_at DESC LIMIT 1) last ON true
 LEFT JOIN LATERAL(SELECT quantity,from_at,through_at,quality FROM meters.intervals WHERE register_id=r.id
  AND through_at<=transaction_timestamp() ORDER BY through_at DESC LIMIT 1) segment ON true
 LEFT JOIN LATERAL(SELECT count(*) total,min(device_id::text) device FROM meters.device_registers
  WHERE register_id=r.id AND started_at<=transaction_timestamp() AND (ended_at IS NULL OR ended_at>transaction_timestamp())) active ON true
 WHERE r.household_id=$1 AND strpos(lower(m.name||' '||r.name),lower($2))>0 ORDER BY m.name,r.name,r.id LIMIT 251`, id.HouseholdID, search)
	if err != nil {
		return nil, err
	}
	return Record{"generation": scope.generation, "timezone": scope.timezone, "now": scope.now,
		"can_write": id.CanWrite(), "can_configure": id.Role == "admin", "meters": meters, "registers": registers}, nil
}

func (s *Store) EnergyManagement(ctx context.Context, id auth.Identity, kind, register string, period EnergyQuery) (Record, error) {
	if register != "" && !value.ValidUUID(register) {
		return nil, Invalid{"Register erneut auswählen"}
	}
	queries := map[string]string{
		"devices": `SELECT to_jsonb(d)||jsonb_build_object('mapping_id',x.id,'mapping_version',x.version,'register_id',x.register_id,
 'started_at',x.started_at,'ended_at',x.ended_at,'initial_value',x.initial_value::text,'final_value',x.final_value::text,
 'rollover_value',x.rollover_value::text,'legacy_boundary',x.legacy_boundary) FROM meters.device_registers x JOIN meters.devices d ON d.id=x.device_id
 WHERE x.household_id=$1 AND ($2='' OR x.register_id=nullif($2,'')::uuid) ORDER BY x.started_at DESC,x.id LIMIT 251`,
		"tariffs": `SELECT to_jsonb(x)||jsonb_build_object('price_minor_per_unit',x.price_minor_per_unit::text,'base_minor_per_day',x.base_minor_per_day::text)
 FROM meters.tariff_versions x WHERE household_id=$1 AND ($2='' OR register_id=nullif($2,'')::uuid) ORDER BY starts_on DESC,id LIMIT 251`,
		"payments": `SELECT to_jsonb(x)||jsonb_build_object('amount_minor',x.amount_minor::text) FROM meters.payments x
 WHERE household_id=$1 AND ($2='' OR register_id=nullif($2,'')::uuid) ORDER BY paid_on DESC,id LIMIT 251`,
		"plans": `SELECT to_jsonb(p)||jsonb_build_object('next_due_on',next.due_on,'due',next.due_on<=(transaction_timestamp() AT TIME ZONE r.timezone)::date)
 FROM meters.reading_plans p JOIN meters.registers r ON r.id=p.register_id LEFT JOIN LATERAL(
 SELECT p.anchor_on+n*p.interval_days AS due_on FROM generate_series(0,999) n
 WHERE NOT EXISTS(SELECT 1 FROM meters.plan_completions c JOIN meters.readings x ON x.id=c.reading_id AND x.version=c.reading_revision
  WHERE c.plan_id=p.id AND c.plan_version=p.version AND c.due_on=p.anchor_on+n*p.interval_days
  AND (NOT p.actual_only OR x.kind='actual')) ORDER BY n LIMIT 1) next ON true
 WHERE p.household_id=$1 AND ($2='' OR p.register_id=nullif($2,'')::uuid) ORDER BY p.anchor_on,p.id LIMIT 251`,
		"sources": `SELECT to_jsonb(x) FROM meters.integration_sources x WHERE household_id=$1
 AND ($2='' OR register_id=nullif($2,'')::uuid) ORDER BY adapter,source_name,id LIMIT 251`,
		"changes": `SELECT to_jsonb(x)||jsonb_build_object('old_final',x.old_final::text,'new_initial',x.new_initial::text)
 FROM meters.device_changes x WHERE household_id=$1 AND ($2='' OR register_id=nullif($2,'')::uuid) ORDER BY changed_at DESC,id LIMIT 251`,
	}
	query, ok := queries[kind]
	if !ok && kind != "bills" {
		return nil, ErrNotFound
	}
	scope, err := s.energyRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	args := []any{id.HouseholdID, register}
	if kind == "tariffs" || kind == "payments" || kind == "bills" {
		if _, _, err = energyWindow(scope, period); err != nil {
			return nil, err
		}
		switch kind {
		case "tariffs":
			query = `SELECT to_jsonb(x)||jsonb_build_object('price_minor_per_unit',x.price_minor_per_unit::text,'base_minor_per_day',x.base_minor_per_day::text)
 FROM meters.tariff_versions x WHERE household_id=$1 AND register_id=$2::uuid
 AND starts_on<=$4::date AND(ends_before IS NULL OR ends_before>$3::date) ORDER BY starts_on DESC,id LIMIT 251`
		case "payments":
			query = `SELECT to_jsonb(x)||jsonb_build_object('amount_minor',x.amount_minor::text) FROM meters.payments x
 WHERE household_id=$1 AND register_id=$2::uuid AND paid_on BETWEEN $3::date AND $4::date ORDER BY paid_on DESC,id LIMIT 251`
		}
		args = append(args, period.From, period.Through)
	}
	if kind == "bills" {
		query = `SELECT to_jsonb(b)||jsonb_build_object('amount_minor',b.amount_minor::text) FROM meters.bills b
 LEFT JOIN core.module_states e ON e.household_id=b.household_id AND e.module_id='expenses'
 WHERE b.household_id=$1 AND b.register_id=$2::uuid AND b.occurred_on BETWEEN $3::date AND $4::date
 AND CASE WHEN b.expense_id IS NULL THEN true WHEN e.state='enabled' AND e.data_generation=b.expense_generation
 THEN core.finance_resource_visible($1,$5,$6,$7::uuid,$8,e.data_generation,'entries',b.expense_id,'view') ELSE false END
 ORDER BY b.occurred_on DESC,b.id LIMIT 251`
		args = append(args, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion)
	}
	rows, err := energyRows(scope.ctx, scope.tx, 250, query, args...)
	if err != nil {
		return nil, err
	}
	return Record{"generation": scope.generation, "rows": rows}, nil
}

type energyHistoryCursor struct {
	Household  string `json:"household"`
	Session    string `json:"session"`
	Generation int64  `json:"generation"`
	Register   string `json:"register"`
	From       string `json:"from"`
	Through    string `json:"through"`
	At         string `json:"at"`
	ID         string `json:"id"`
}

func energyWindow(scope *energyReadScope, query EnergyQuery) (time.Time, time.Time, error) {
	if !value.ValidUUID(query.RegisterID) || !value.Date(query.From) || !value.Date(query.Through) {
		return time.Time{}, time.Time{}, Invalid{"Register und beide Kalenderdaten angeben"}
	}
	var zone string
	err := scope.tx.QueryRow(scope.ctx, `SELECT timezone FROM meters.registers WHERE household_id=$1 AND id=$2`, scope.household, query.RegisterID).Scan(&zone)
	if err != nil {
		return time.Time{}, time.Time{}, energyError(err)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	first, err := time.Parse(time.DateOnly, query.From)
	if err != nil {
		return time.Time{}, time.Time{}, Invalid{"Zeitraum prüfen"}
	}
	last, err := time.Parse(time.DateOnly, query.Through)
	if err != nil || last.Before(first) || last.Sub(first)/(24*time.Hour) >= 366 {
		return time.Time{}, time.Time{}, Invalid{"Zeitraum auf höchstens366Kalendertage begrenzen"}
	}
	from, _, err := civiltime.Resolve(first, "00:00", location)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, _, err := civiltime.Resolve(last.AddDate(0, 0, 1), "00:00", location)
	if err != nil || !from.Before(end) {
		return time.Time{}, time.Time{}, Invalid{"Zeitraum hat in dieser Zeitzone keine tatsächliche Dauer"}
	}
	return from, end, nil
}

func (s *Store) EnergyHistory(ctx context.Context, id auth.Identity, query EnergyQuery) (Record, error) {
	if !value.ValidUUID(query.RegisterID) {
		return nil, Invalid{"Register erneut auswählen"}
	}
	scope, err := s.energyRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	var registerExists bool
	err = scope.tx.QueryRow(scope.ctx, `SELECT EXISTS(SELECT 1 FROM meters.registers WHERE household_id=$1 AND id=$2)`, id.HouseholdID, query.RegisterID).Scan(&registerExists)
	if err != nil || !registerExists {
		return nil, ErrNotFound
	}
	from, through, err := energyWindow(scope, query)
	if err != nil {
		return nil, err
	}
	cursor := energyHistoryCursor{Household: id.HouseholdID, Session: id.SessionID, Generation: scope.generation,
		Register: query.RegisterID, From: query.From, Through: query.Through}
	if query.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		var previous energyHistoryCursor
		if err != nil || len(raw) > 2000 || json.Unmarshal(raw, &previous) != nil {
			return nil, Invalid{"Ungültige Messwertseite"}
		}
		if previous.Household != cursor.Household || previous.Session != cursor.Session || previous.Generation != cursor.Generation ||
			previous.Register != cursor.Register || previous.From != cursor.From || previous.Through != cursor.Through || len(previous.ID) > 80 {
			return nil, ErrConflict
		}
		if _, err := time.Parse(time.RFC3339Nano, previous.At); err != nil {
			return nil, Invalid{"Ungültige Messwertseite"}
		}
		cursor = previous
	}
	rows, err := energyRows(scope.ctx, scope.tx, 101, `SELECT jsonb_build_object('id',observed.id,'device_id',observed.device_id,'measured_at',observed.measured_at,
 'value',observed.value::text,'kind',observed.kind,'version',observed.version,'interval_started_at',observed.interval_started_at,'origin',observed.origin,
 'note',observed.note,'rollover_confirmed',observed.rollover_confirmed)
 FROM meters.registers r JOIN LATERAL(
 SELECT 'r/'||x.id::text id,x.device_id,x.measured_at,x.value,x.kind,x.version,x.interval_started_at,
 CASE WHEN x.source_key LIKE 'manual/csv/%' THEN 'csv' ELSE 'manual' END::text origin,x.note,x.rollover_confirmed
 FROM meters.readings x WHERE x.register_id=r.id AND r.input_mode='manual' AND x.measured_at>=$3 AND x.measured_at<$4
 UNION ALL SELECT 't/'||x.id::text,x.device_id,x.measured_at,x.value,x.kind,1::bigint,x.interval_started_at,'telemetry',''::text,x.rollover_confirmed
 FROM meters.telemetry x WHERE x.register_id=r.id AND r.input_mode='telemetry' AND x.measured_at>=$3 AND x.measured_at<$4) observed ON true
 WHERE r.household_id=$1 AND r.id=$2 AND ($5='' OR (observed.measured_at,observed.id)>(nullif($5,'')::timestamptz,$6))
 ORDER BY observed.measured_at,observed.id LIMIT 101`, id.HouseholdID, query.RegisterID, from, through, cursor.At, cursor.ID)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > 100 {
		rows = rows[:100]
		cursor.At = String(rows[99]["measured_at"])
		cursor.ID = String(rows[99]["id"])
		raw, err := json.Marshal(cursor)
		if err != nil {
			return nil, err
		}
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return Record{"generation": scope.generation, "rows": rows, "next_cursor": next, "from": from, "through": through,
		"register_id": query.RegisterID, "private_history": true, "filter": strings.TrimSpace(query.Search)}, nil
}
