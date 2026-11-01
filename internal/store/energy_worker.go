package store

import (
	"context"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/energy"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const EnergyWorkspaceJobKind = "energy_workspace"

func (s *Store) ScheduleEnergyWorkspace(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := audit.Exec(audit.WithSystem(ctx, "worker"), s.Pool, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,dedupe_key)
 SELECT state.household_id,'meters',state.data_generation,$1,
 'energy/'||state.household_id::text||'/'||state.data_generation::text||'/'||to_char(clock_timestamp(),'YYYY-MM-DD-HH24-MI')
 FROM core.module_states state WHERE state.module_id='meters' AND state.state='enabled'
 AND(EXISTS(SELECT 1 FROM meters.dirty_ranges d WHERE d.household_id=state.household_id)
 OR EXISTS(SELECT 1 FROM meters.registers r JOIN meters.telemetry t ON t.register_id=r.id
  WHERE r.household_id=state.household_id AND r.retention_days IS NOT NULL AND r.input_mode='telemetry'
  AND t.measured_at<clock_timestamp()-make_interval(days=>r.retention_days)
  AND EXISTS(SELECT 1 FROM meters.telemetry later WHERE later.register_id=r.id AND later.device_id=t.device_id
   AND later.measured_at>t.measured_at AND later.measured_at<clock_timestamp()-make_interval(days=>r.retention_days))
  AND EXISTS(SELECT 1 FROM meters.aggregates a WHERE a.register_id=r.id AND a.bucket_kind='hour' AND a.from_at<=t.measured_at
   AND a.through_at>t.measured_at AND a.quantity IS NOT NULL AND a.covered_seconds>0)))
 AND NOT EXISTS(SELECT 1 FROM core.jobs j WHERE j.household_id=state.household_id AND j.module_id='meters'
 AND j.module_generation=state.data_generation AND j.kind=$1 AND j.state IN('pending','running') AND j.attempts<j.max_attempts)
 ORDER BY state.household_id LIMIT 50 ON CONFLICT(dedupe_key) DO NOTHING`, EnergyWorkspaceJobKind)
	return err
}

func (s *Store) CompleteEnergyWorkspaceJob(ctx context.Context, job, lease string) error {
	if !value.ValidUUID(job) || !value.ValidUUID(lease) {
		return ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var household string
	err = tx.QueryRow(ctx, `SELECT household_id FROM core.jobs WHERE id=$1 AND lease_token=$2 AND state='running'
 AND lease_until>clock_timestamp() AND kind=$3`, job, lease, EnergyWorkspaceJobKind).Scan(&household)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return err
	}
	var generation, jobGeneration int64
	var state string
	err = tx.QueryRow(ctx, `SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id='meters' FOR SHARE`, household).
		Scan(&state, &generation)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('household/meters/'||$1::text,0))`, household); err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT module_generation FROM core.jobs WHERE id=$1 AND lease_token=$2 AND kind=$3
 AND module_id='meters' AND household_id=$4 AND state='running' AND lease_until>clock_timestamp() FOR UPDATE`,
		job, lease, EnergyWorkspaceJobKind, household).Scan(&jobGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "enabled" || generation != jobGeneration {
		_, err = tx.Exec(ctx, `UPDATE core.jobs SET state='paused',lease_until=NULL,lease_token=NULL,finished_at=clock_timestamp()
 WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp()`, job, lease)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	dirty, err := energyRows(ctx, tx, 1, `SELECT to_jsonb(d)||jsonb_build_object('measurement_kind',r.measurement_kind,'unit_code',r.unit_code,
 'timezone',r.timezone,'input_mode',r.input_mode,'max_gap_seconds',r.max_gap_seconds,'created_by',r.created_by)
 FROM meters.dirty_ranges d JOIN meters.registers r ON r.id=d.register_id
 WHERE d.household_id=$1 ORDER BY d.updated_at,d.register_id LIMIT 1 FOR UPDATE OF d`, household)
	if err != nil {
		return err
	}
	if len(dirty) != 0 {
		if String(dirty[0]["phase"]) == "intervals" {
			err = energyRebuildIntervals(ctx, tx, household, dirty[0])
		} else {
			err = energyRebuildAggregates(ctx, tx, household, dirty[0])
		}
		if err != nil {
			return err
		}
	} else {
		registers, err := energyRows(ctx, tx, 2, `SELECT jsonb_build_object('id',r.id) FROM meters.registers r
 WHERE r.household_id=$1 AND r.input_mode='telemetry' AND r.retention_days IS NOT NULL
 AND EXISTS(SELECT 1 FROM meters.telemetry t WHERE t.register_id=r.id AND t.measured_at<clock_timestamp()-make_interval(days=>r.retention_days)
  AND EXISTS(SELECT 1 FROM meters.telemetry later WHERE later.register_id=r.id AND later.device_id=t.device_id AND later.measured_at>t.measured_at
   AND later.measured_at<clock_timestamp()-make_interval(days=>r.retention_days))
  AND EXISTS(SELECT 1 FROM meters.aggregates a WHERE a.register_id=r.id AND a.bucket_kind='hour' AND a.from_at<=t.measured_at
   AND a.through_at>t.measured_at AND a.quantity IS NOT NULL AND a.covered_seconds>0)) ORDER BY r.updated_at,r.id LIMIT 2`, household)
		if err != nil {
			return err
		}
		for _, register := range registers {
			var removed int
			if err = tx.QueryRow(ctx, `SELECT meters.prune_telemetry($1,$2,$3,$4,500)`, household, register["id"], job, lease).Scan(&removed); err != nil {
				return err
			}
		}
	}
	var more bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meters.dirty_ranges WHERE household_id=$1)
 OR EXISTS(SELECT 1 FROM meters.registers r JOIN meters.telemetry t ON t.register_id=r.id
 WHERE r.household_id=$1 AND r.retention_days IS NOT NULL AND t.measured_at<clock_timestamp()-make_interval(days=>r.retention_days)
 AND EXISTS(SELECT 1 FROM meters.telemetry later WHERE later.register_id=r.id AND later.device_id=t.device_id AND later.measured_at>t.measured_at
  AND later.measured_at<clock_timestamp()-make_interval(days=>r.retention_days))
 AND EXISTS(SELECT 1 FROM meters.aggregates a WHERE a.register_id=r.id AND a.bucket_kind='hour' AND a.from_at<=t.measured_at
  AND a.through_at>t.measured_at AND a.quantity IS NOT NULL AND a.covered_seconds>0))`, household).Scan(&more); err != nil {
		return err
	}
	finished := "done"
	if more {
		finished = "pending"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,finished_at=CASE WHEN $3='done' THEN clock_timestamp() END,
 available_at=clock_timestamp(),lease_token=NULL,lease_until=NULL,attempts=0
 WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>clock_timestamp()`, job, lease, finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

const energyPointSQL = `WITH observed AS(
 SELECT x.id::text id,x.device_id,x.measured_at,x.interval_started_at,x.value,x.kind,x.rollover_confirmed,
 CASE WHEN d.ended_at=x.measured_at THEN '0/r/' ELSE '2/r/' END||x.id::text point_key,d.rollover_value
 FROM meters.readings x JOIN meters.device_registers d ON d.register_id=x.register_id AND d.device_id=x.device_id
 WHERE x.household_id=$1 AND x.register_id=$2 AND $3='manual'
 UNION ALL SELECT x.id::text,x.device_id,x.measured_at,x.interval_started_at,x.value,x.kind,x.rollover_confirmed,
 CASE WHEN d.ended_at=x.measured_at THEN '0/t/' ELSE '2/t/' END||x.id::text,d.rollover_value
 FROM meters.telemetry x JOIN meters.device_registers d ON d.register_id=x.register_id AND d.device_id=x.device_id
 WHERE x.household_id=$1 AND x.register_id=$2 AND $3='telemetry' AND EXISTS(SELECT 1 FROM meters.registers r WHERE r.id=x.register_id
  AND(r.retained_from IS NULL OR x.measured_at>=r.retained_from OR (d.ended_at IS NULL OR d.ended_at>=r.retained_from)
  AND x.id=(SELECT anchor.id FROM meters.telemetry anchor WHERE anchor.register_id=x.register_id AND anchor.device_id=x.device_id
   AND anchor.measured_at<r.retained_from ORDER BY anchor.measured_at DESC,anchor.id DESC LIMIT 1)))
 UNION ALL SELECT d.id::text,d.device_id,d.ended_at,NULL,d.final_value,
 CASE WHEN d.legacy_boundary THEN 'estimated' ELSE 'actual' END,false,'0/f/'||d.id::text,d.rollover_value
 FROM meters.device_registers d JOIN meters.registers r ON r.id=d.register_id
 WHERE d.household_id=$1 AND d.register_id=$2 AND r.measurement_kind='cumulative' AND d.ended_at IS NOT NULL AND d.final_value IS NOT NULL
 AND(r.retained_from IS NULL OR d.ended_at>=r.retained_from)
 AND NOT EXISTS(SELECT 1 FROM meters.readings x WHERE x.register_id=d.register_id AND x.device_id=d.device_id AND x.measured_at=d.ended_at AND $3='manual')
 AND NOT EXISTS(SELECT 1 FROM meters.telemetry x WHERE x.register_id=d.register_id AND x.device_id=d.device_id AND x.measured_at=d.ended_at AND $3='telemetry')
 UNION ALL SELECT d.id::text,d.device_id,d.started_at,NULL,d.initial_value,
 CASE WHEN d.legacy_boundary THEN 'estimated' ELSE 'actual' END,false,'1/b/'||d.id::text,d.rollover_value
 FROM meters.device_registers d JOIN meters.registers r ON r.id=d.register_id
 WHERE d.household_id=$1 AND d.register_id=$2 AND r.measurement_kind='cumulative' AND(r.retained_from IS NULL OR d.started_at>=r.retained_from)
 AND NOT EXISTS(SELECT 1 FROM meters.readings x WHERE x.register_id=d.register_id AND x.device_id=d.device_id AND x.measured_at=d.started_at AND $3='manual')
 AND NOT EXISTS(SELECT 1 FROM meters.telemetry x WHERE x.register_id=d.register_id AND x.device_id=d.device_id AND x.measured_at=d.started_at AND $3='telemetry'))
 SELECT jsonb_build_object('id',id,'device_id',device_id,'measured_at',measured_at,'interval_started_at',interval_started_at,
 'value',value::text,'kind',kind,'rollover_confirmed',rollover_confirmed,'point_key',point_key,'rollover_value',rollover_value::text)
 FROM observed `

func energyTime(text string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || at.Year() < 1900 || at.Year() > 2200 {
		return time.Time{}, Invalid{"Gespeicherten Zählerzeitpunkt prüfen"}
	}
	return at, nil
}

func energyPoint(record Record) (energy.Point, error) {
	point := energy.Point{ID: String(record["id"]), DeviceID: String(record["device_id"]), Value: String(record["value"]),
		Kind: String(record["kind"]), RolloverConfirmed: record["rollover_confirmed"] == true}
	at, err := energyTime(String(record["measured_at"]))
	if err != nil {
		return point, err
	}
	point.At = at
	if text := String(record["interval_started_at"]); text != "" {
		start, err := energyTime(text)
		if err != nil {
			return point, err
		}
		point.StartedAt = &start
	}
	return point, nil
}

func energyRebuildIntervals(ctx context.Context, tx pgx.Tx, household string, dirty Record) error {
	register, mode := String(dirty["register_id"]), String(dirty["input_mode"])
	from, err := energyTime(String(dirty["from_at"]))
	if err != nil {
		return err
	}
	through, err := energyTime(String(dirty["through_at"]))
	if err != nil {
		return err
	}
	cursor, cursorID := from, ""
	if text := String(dirty["cursor_at"]); text != "" {
		cursor, err = energyTime(text)
		if err != nil {
			return err
		}
		cursorID = String(dirty["cursor_id"])
	}
	before, err := energyRows(ctx, tx, 1, energyPointSQL+`WHERE (measured_at,point_key)<($4::timestamptz,$5)
 ORDER BY measured_at DESC,point_key DESC LIMIT 1`, household, register, mode, cursor, cursorID)
	if cursorID != "" {
		before, err = energyRows(ctx, tx, 1, energyPointSQL+`WHERE (measured_at,point_key)<=($4::timestamptz,$5)
 ORDER BY measured_at DESC,point_key DESC LIMIT 1`, household, register, mode, cursor, cursorID)
	}
	if err != nil {
		return err
	}
	rows, err := energyRows(ctx, tx, 513, energyPointSQL+`WHERE (measured_at,point_key)>($4::timestamptz,$5)
 AND measured_at<=$6 ORDER BY measured_at,point_key LIMIT 513`, household, register, mode, cursor, cursorID, through)
	if err != nil {
		return err
	}
	more := len(rows) > 512
	if more {
		rows = rows[:512]
	}
	points := append(before, rows...)
	clearFrom, clearThrough := from, through
	if len(before) != 0 {
		clearFrom, err = energyTime(String(before[0]["measured_at"]))
		if err != nil {
			return err
		}
	}
	if more {
		clearThrough, err = energyTime(String(rows[len(rows)-1]["measured_at"]))
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM meters.intervals WHERE household_id=$1 AND register_id=$2 AND from_at<$4 AND through_at>$3`,
		household, register, clearFrom, clearThrough); err != nil {
		return err
	}
	for index, row := range points {
		point, err := energyPoint(row)
		if err != nil {
			return err
		}
		previous := point
		if index > 0 {
			previous, err = energyPoint(points[index-1])
			if err != nil {
				return err
			}
		}
		if String(dirty["measurement_kind"]) != "interval" && (index == 0 || !previous.At.Before(point.At)) {
			continue
		}
		if len(before) != 0 && index == 0 {
			continue
		}
		interval, err := energy.Between(String(dirty["measurement_kind"]), String(dirty["unit_code"]), previous, point,
			String(row["rollover_value"]), time.Duration(Int(dirty["max_gap_seconds"]))*time.Second)
		if err != nil {
			return Invalid{"Messreihe enthält einen ungültigen Typ, Zeitraum oder Dezimalwert"}
		}
		var quantity any
		if interval.Quantity != nil {
			quantity = interval.Quantity.FloatString(9)
		}
		_, err = tx.Exec(ctx, `INSERT INTO meters.intervals(household_id,register_id,from_at,through_at,device_id,quantity,unit_code,quality,
 observation_kind,source_revision,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
 ON CONFLICT(register_id,from_at,through_at,observation_kind) DO UPDATE SET device_id=EXCLUDED.device_id,quantity=EXCLUDED.quantity,
 unit_code=EXCLUDED.unit_code,quality=EXCLUDED.quality,source_revision=EXCLUDED.source_revision`,
			household, register, interval.From, interval.Through, interval.DeviceID, quantity, interval.Unit, interval.Quality,
			mode, Int(dirty["source_revision"]), dirty["created_by"])
		if err != nil {
			return err
		}
	}
	if more {
		last := rows[len(rows)-1]
		_, err = tx.Exec(ctx, `UPDATE meters.dirty_ranges SET cursor_at=$3,cursor_id=$4,updated_at=clock_timestamp()
 WHERE household_id=$1 AND register_id=$2`, household, register, last["measured_at"], last["point_key"])
		return err
	}
	start, _, err := energy.BucketSpan(from, String(dirty["timezone"]), "hour")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE meters.dirty_ranges SET phase='aggregates',cursor_at=$3,cursor_id='',updated_at=clock_timestamp()
 WHERE household_id=$1 AND register_id=$2`, household, register, start)
	return err
}
