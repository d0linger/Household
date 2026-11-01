package store

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"household/internal/audit"
	"household/internal/meterfeed"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const EnergyAdapterJobKind = "energy_adapter"

var ErrEnergyFeedRate = errors.New("meter source minute budget exhausted")

type EnergyAdapterLease struct {
	JobID         string
	Lease         string
	HouseholdID   string
	Generation    int64
	SourceID      string
	SourceVersion int64
	Adapter       string
	Source        meterfeed.Source
}

func (s *Store) ScheduleEnergyAdapters(ctx context.Context, hasHA, hasMQTT bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = audit.WithSystem(ctx, "worker")
	_, err := audit.Exec(ctx, s.Pool, `UPDATE meters.integration_sources source SET status_code='approval_required'
 WHERE source.enabled AND source.status_code IS DISTINCT FROM 'approval_required' AND NOT EXISTS(
 SELECT 1 FROM meters.registers r JOIN meters.meters meter ON meter.id=r.meter_id AND meter.household_id=r.household_id
 JOIN core.memberships member ON member.household_id=r.household_id AND member.user_id=source.authorized_by
 JOIN core.users u ON u.id=member.user_id WHERE r.id=source.register_id AND r.household_id=source.household_id
 AND r.archived_at IS NULL AND meter.archived_at IS NULL AND member.role='admin' AND member.inactive_at IS NULL
 AND u.disabled_at IS NULL AND NOT u.must_change_password)`)
	if err != nil {
		return err
	}
	_, err = audit.Exec(ctx, s.Pool, `UPDATE meters.integration_sources SET status_code='configuration_missing'
 WHERE enabled AND status_code IS DISTINCT FROM 'configuration_missing'
 AND((adapter='home_assistant' AND NOT $1) OR(adapter='mqtt' AND NOT $2))`, hasHA, hasMQTT)
	if err != nil {
		return err
	}
	_, err = audit.Exec(ctx, s.Pool, `UPDATE core.jobs j SET state='paused',lease_token=NULL,lease_until=NULL,
 finished_at=clock_timestamp(),last_error='source_changed' WHERE j.kind=$1 AND j.state IN('pending','running')
 AND NOT EXISTS(SELECT 1 FROM meters.integration_sources source JOIN core.module_states m ON m.household_id=source.household_id AND m.module_id='meters'
 JOIN meters.registers r ON r.id=source.register_id AND r.household_id=source.household_id AND r.archived_at IS NULL
 JOIN meters.meters meter ON meter.id=r.meter_id AND meter.household_id=r.household_id AND meter.archived_at IS NULL
 JOIN core.memberships member ON member.household_id=source.household_id AND member.user_id=source.authorized_by AND member.role='admin' AND member.inactive_at IS NULL
 JOIN core.users u ON u.id=member.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password
 WHERE source.id::text=j.payload->>'source_id' AND source.version::text=j.payload->>'source_version' AND source.enabled
 AND source.household_id=j.household_id AND source.module_generation=j.module_generation AND m.state='enabled' AND m.data_generation=j.module_generation
 AND((source.adapter='home_assistant' AND $2) OR(source.adapter='mqtt' AND $3)))`, EnergyAdapterJobKind, hasHA, hasMQTT)
	if err != nil {
		return err
	}
	_, err = audit.Exec(ctx, s.Pool, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,dedupe_key,payload)
 SELECT source.household_id,'meters',source.module_generation,$1,
 'meter-feed/'||source.id::text||'/'||source.version::text||'/'||source.module_generation::text||'/'||to_char(clock_timestamp(),'YYYY-MM-DD-HH24-MI'),
 jsonb_build_object('source_id',source.id,'source_version',source.version)
 FROM meters.integration_sources source JOIN core.module_states m ON m.household_id=source.household_id AND m.module_id='meters'
 JOIN meters.registers r ON r.id=source.register_id AND r.household_id=source.household_id AND r.archived_at IS NULL
 JOIN meters.meters meter ON meter.id=r.meter_id AND meter.household_id=r.household_id AND meter.archived_at IS NULL
 JOIN core.memberships member ON member.household_id=source.household_id AND member.user_id=source.authorized_by
 JOIN core.users u ON u.id=member.user_id WHERE source.enabled AND m.state='enabled' AND m.data_generation=source.module_generation
 AND member.role='admin' AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND coalesce(source.next_attempt_at,clock_timestamp())<=clock_timestamp()
 AND((source.adapter='home_assistant' AND $2) OR(source.adapter='mqtt' AND $3))
 AND NOT EXISTS(SELECT 1 FROM core.jobs j WHERE j.kind=$1 AND j.household_id=source.household_id AND j.module_generation=source.module_generation
 AND j.payload->>'source_id'=source.id::text AND j.payload->>'source_version'=source.version::text
 AND j.state IN('pending','running') AND j.attempts<j.max_attempts)
 ORDER BY source.next_attempt_at NULLS FIRST,source.id LIMIT 32 ON CONFLICT(dedupe_key) DO NOTHING`, EnergyAdapterJobKind, hasHA, hasMQTT)
	return err
}

func (s *Store) ClaimEnergyAdapter(ctx context.Context) (*EnergyAdapterLease, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.Pool)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	lease := &EnergyAdapterLease{Lease: value.UUID()}
	err = tx.QueryRow(ctx, `WITH candidate AS(SELECT j.id,source.id source_id,source.version source_version,source.adapter,
 source.source_name,source.measurement_kind,source.unit_code FROM core.jobs j
 JOIN meters.integration_sources source ON source.id::text=j.payload->>'source_id' AND source.version::text=j.payload->>'source_version'
 JOIN meters.registers r ON r.id=source.register_id AND r.household_id=source.household_id AND r.archived_at IS NULL
 JOIN meters.meters meter ON meter.id=r.meter_id AND meter.household_id=r.household_id AND meter.archived_at IS NULL
 JOIN core.module_states m ON m.household_id=j.household_id AND m.module_id=j.module_id
 JOIN core.memberships member ON member.household_id=source.household_id AND member.user_id=source.authorized_by
 JOIN core.users u ON u.id=member.user_id WHERE j.kind=$1 AND j.module_id='meters' AND source.enabled
 AND source.household_id=j.household_id AND source.module_generation=j.module_generation AND m.state='enabled' AND m.data_generation=j.module_generation
 AND member.role='admin' AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND((j.state='pending' AND j.available_at<=clock_timestamp()) OR(j.state='running' AND j.lease_until<clock_timestamp()))
 AND j.attempts<j.max_attempts ORDER BY j.available_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED)
 UPDATE core.jobs j SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1
 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.household_id,j.module_generation,c.source_id,c.source_version,c.adapter,
 c.source_name,c.measurement_kind,c.unit_code`, EnergyAdapterJobKind, lease.Lease).
		Scan(&lease.JobID, &lease.HouseholdID, &lease.Generation, &lease.SourceID, &lease.SourceVersion, &lease.Adapter,
			&lease.Source.Name, &lease.Source.Kind, &lease.Source.Unit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lease.Source.ID, lease.Source.Generation = lease.SourceID, lease.Generation
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *Store) energyAdapterTransaction(ctx context.Context, lease EnergyAdapterLease) (pgx.Tx, Record, error) {
	if !value.ValidUUID(lease.JobID) || !value.ValidUUID(lease.Lease) || !value.ValidUUID(lease.HouseholdID) || !value.ValidUUID(lease.SourceID) {
		return nil, nil, ErrForbidden
	}
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.Pool)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (pgx.Tx, Record, error) { rollback(tx); return nil, nil, err }
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, lease.HouseholdID); err != nil {
		return fail(err)
	}
	var generation int64
	var state string
	err = tx.QueryRow(ctx, `SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id='meters' FOR SHARE`,
		lease.HouseholdID).Scan(&state, &generation)
	if err != nil {
		return fail(err)
	}
	if state != "enabled" || generation != lease.Generation {
		return fail(ErrConflict)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('household/meters/'||$1::text,0))`, lease.HouseholdID); err != nil {
		return fail(err)
	}
	source, err := energyObject(ctx, tx, `SELECT to_jsonb(source)||jsonb_build_object('retained_from',r.retained_from)
 FROM meters.integration_sources source JOIN meters.registers r ON r.id=source.register_id AND r.household_id=source.household_id AND r.archived_at IS NULL
 JOIN meters.meters meter ON meter.id=r.meter_id AND meter.household_id=r.household_id AND meter.archived_at IS NULL
 JOIN core.memberships m ON m.household_id=source.household_id AND m.user_id=source.authorized_by JOIN core.users u ON u.id=m.user_id
 WHERE source.household_id=$1 AND source.id=$2 AND source.version=$3 AND source.module_generation=$4 AND source.enabled
 AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 FOR SHARE OF source,m,u,r,meter`, lease.HouseholdID, lease.SourceID, lease.SourceVersion, lease.Generation)
	if err != nil {
		return fail(energyError(err))
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.jobs WHERE id=$1 AND lease_token=$2 AND kind=$3 AND household_id=$4
 AND module_id='meters' AND module_generation=$5 AND state='running' AND lease_until>clock_timestamp()
 AND payload->>'source_id'=$6 AND payload->>'source_version'=$7 FOR UPDATE`, lease.JobID, lease.Lease,
		EnergyAdapterJobKind, lease.HouseholdID, lease.Generation, lease.SourceID, strconv.FormatInt(lease.SourceVersion, 10)).Scan(&valid)
	if err != nil {
		return fail(energyError(err))
	}
	return tx, source, nil
}

func energyAdapterLeaseCurrent(ctx context.Context, tx pgx.Tx, lease EnergyAdapterLease) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT true FROM core.jobs WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>clock_timestamp()
 AND household_id=$3 AND module_id='meters' AND module_generation=$4`, lease.JobID, lease.Lease, lease.HouseholdID, lease.Generation).Scan(&valid)
	if err != nil || !valid {
		return ErrConflict
	}
	return nil
}

func (s *Store) IngestEnergySample(ctx context.Context, lease EnergyAdapterLease, sample meterfeed.Sample) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, source, err := s.energyAdapterTransaction(ctx, lease)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	if meterfeed.ValidateSample(sample, lease.Source) != nil || source["measurement_kind"] != sample.MeasurementKind || source["unit_code"] != sample.Unit {
		return false, Invalid{"Adaptermessung passt nicht zur aktuellen Zuordnung"}
	}
	key, err := sample.Key()
	if err != nil {
		return false, err
	}
	var duplicate, exact bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meters.telemetry WHERE register_id=$1 AND device_id=$2 AND measured_at=$3),
 EXISTS(SELECT 1 FROM meters.telemetry WHERE register_id=$1 AND device_id=$2 AND measured_at=$3 AND value=$4::numeric AND kind=$5
 AND interval_started_at IS NOT DISTINCT FROM $6::timestamptz AND rollover_confirmed=$7)`, source["register_id"], source["device_id"],
		sample.MeasuredAt, sample.Value, sample.Quality, sample.IntervalStartedAt, sample.RolloverConfirmed).Scan(&duplicate, &exact)
	if err != nil {
		return false, err
	}
	if duplicate {
		if !exact {
			return false, Invalid{"Adapter liefert widersprüchliche Werte am selben Messzeitpunkt"}
		}
		return false, energyAdapterLeaseCurrent(ctx, tx, lease)
	}
	var permitted bool
	err = tx.QueryRow(ctx, `UPDATE meters.integration_sources SET minute_started_at=date_trunc('minute',clock_timestamp()),
 minute_samples=CASE WHEN minute_started_at=date_trunc('minute',clock_timestamp()) THEN minute_samples+1 ELSE 1 END,
 last_attempt_at=clock_timestamp() WHERE household_id=$1 AND id=$2
 AND(minute_started_at IS DISTINCT FROM date_trunc('minute',clock_timestamp()) OR minute_samples<120) RETURNING true`, lease.HouseholdID, lease.SourceID).Scan(&permitted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrEnergyFeedRate
	}
	if err != nil || !permitted {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO meters.telemetry(household_id,register_id,device_id,source_id,source_key,measured_at,interval_started_at,
 value,kind,rollover_confirmed,created_by,source_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, lease.HouseholdID,
		source["register_id"], source["device_id"], lease.SourceID, key, sample.MeasuredAt, sample.IntervalStartedAt,
		sample.Value, sample.Quality, sample.RolloverConfirmed, source["authorized_by"], lease.SourceVersion)
	if err != nil {
		return false, energyError(err)
	}
	if err = energyAdapterLeaseCurrent(ctx, tx, lease); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) RejectEnergySample(ctx context.Context, lease EnergyAdapterLease, digest string) error {
	if len(digest) != 64 {
		return Invalid{"Ablehnungsnachweis ist ungültig"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.energyAdapterTransaction(ctx, lease)
	if err != nil {
		return err
	}
	defer rollback(tx)
	_, err = tx.Exec(ctx, `UPDATE meters.integration_sources SET last_rejected_sha256=$3,rejected_messages=rejected_messages+1,
 status_code='invalid_message',last_attempt_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, lease.HouseholdID, lease.SourceID, digest)
	if err == nil {
		err = energyAdapterLeaseCurrent(ctx, tx, lease)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return err
}

func (s *Store) FinishEnergyAdapter(ctx context.Context, lease EnergyAdapterLease, code string, stats meterfeed.MQTTStats) error {
	if !slices.Contains([]string{"ready", "configuration_missing", "unavailable", "invalid_message", "backoff"}, code) {
		return ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.energyAdapterTransaction(ctx, lease)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		_, err = audit.Exec(audit.WithSystem(ctx, "worker"), s.Pool, `UPDATE core.jobs SET state='paused',last_error='source_changed',
 lease_token=NULL,lease_until=NULL,finished_at=clock_timestamp() WHERE id=$1 AND lease_token=$2 AND kind=$3`,
			lease.JobID, lease.Lease, EnergyAdapterJobKind)
		return err
	}
	if err != nil {
		return err
	}
	defer rollback(tx)
	if code == "ready" && stats.Rejected > 0 {
		code = "invalid_message"
	}
	more := stats.Packets >= 100 && (code == "ready" || code == "invalid_message")
	var qos any
	if stats.Subscribed {
		qos = int(stats.QoS)
	}
	_, err = tx.Exec(ctx, `UPDATE meters.integration_sources SET status_code=$3,last_attempt_at=clock_timestamp(),
 last_success_at=CASE WHEN $3 IN('ready','invalid_message') THEN clock_timestamp() ELSE last_success_at END,
 consecutive_failures=CASE WHEN $3 IN('ready','invalid_message','backoff') THEN 0 ELSE least(20,consecutive_failures+1) END,
 next_attempt_at=CASE WHEN $4 THEN clock_timestamp() WHEN $3='backoff' THEN date_trunc('minute',clock_timestamp())+interval '1 minute'
 WHEN $3 IN('ready','invalid_message') THEN clock_timestamp()+interval '30 seconds'
 ELSE clock_timestamp()+make_interval(secs=>least(3600,30*(1<<least(7,consecutive_failures+1)))) END,
 negotiated_qos=coalesce($5::integer,negotiated_qos)
 WHERE household_id=$1 AND id=$2`, lease.HouseholdID, lease.SourceID, code, more, qos)
	if err != nil {
		return err
	}
	state := "done"
	if more {
		state = "pending"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,finished_at=CASE WHEN $3='done' THEN clock_timestamp() END,
 available_at=clock_timestamp(),lease_token=NULL,lease_until=NULL,attempts=0,last_error=CASE WHEN $4='ready' THEN NULL ELSE $4 END
 WHERE id=$1 AND lease_token=$2 AND kind=$5 AND state='running' AND lease_until>clock_timestamp()`,
		lease.JobID, lease.Lease, state, code, EnergyAdapterJobKind)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
