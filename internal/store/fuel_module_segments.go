package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const fuelSegmentAlgorithm = 1
const fuelSegmentChunk = 500
const fuelSegmentCurrencyLimit = 64

// The pending point is deliberately carried across chunk boundaries. A second
// receipt with the same date and counter must invalidate that anchor before an
// interval is published, even when the duplicate arrives in the next chunk.
type fuelSegmentCheckpoint struct {
	Algorithm      int               `json:"algorithm"`
	AsOf           string            `json:"as_of"`
	SeriesID       string            `json:"series_id"`
	Pending        *fuelSegmentPoint `json:"pending,omitempty"`
	PendingTied    bool              `json:"pending_tied"`
	Anchor         *fuelSegmentPoint `json:"anchor,omitempty"`
	Previous       *fuelSegmentPoint `json:"previous,omitempty"`
	Quantity       string            `json:"quantity"`
	Costs          map[string]string `json:"costs"`
	CostOverflow   bool              `json:"cost_overflow"`
	CostUnknown    bool              `json:"cost_unknown"`
	Blocked        bool              `json:"blocked"`
	Reason         string            `json:"reason"`
	LastFuel       string            `json:"last_fuel"`
	Rows           int64             `json:"rows"`
	CursorSeries   string            `json:"cursor_series"`
	CursorDay      string            `json:"cursor_day"`
	CursorOdometer string            `json:"cursor_odometer"`
	CursorID       string            `json:"cursor_id"`
}

type fuelSegmentPoint struct {
	ID, VehicleID, SeriesID, Date, Odometer, Quantity string
	Fuel, Unit, Currency, CostMinor                   string
	Initial, Started                                  string
	Ended                                             *string
	Full, Missing                                     bool
	Version                                           int64
}

type fuelComputedSegment struct {
	VehicleID, SeriesID, FromID, ToID, From, To string
	FromReading, ToReading                      string
	Fuel, Unit, State                           string
	Quantity, Distance, Per100                  *string
	Costs                                       map[string]string
	BelowPrecision                              bool
	CostUnknown                                 bool
}

func fuelEmptyCheckpoint() fuelSegmentCheckpoint {
	return fuelSegmentCheckpoint{Algorithm: fuelSegmentAlgorithm, Quantity: "0.000000", Costs: map[string]string{}, Reason: "no_full_anchor"}
}

func fuelSegmentReset(checkpoint *fuelSegmentCheckpoint, reason string) {
	checkpoint.Anchor = nil
	checkpoint.Quantity = "0.000000"
	checkpoint.Costs = map[string]string{}
	checkpoint.CostOverflow = false
	checkpoint.CostUnknown = false
	checkpoint.Reason = reason
}

func fuelSegmentGap(point fuelSegmentPoint, reason string) fuelComputedSegment {
	return fuelComputedSegment{VehicleID: point.VehicleID, SeriesID: point.SeriesID, FromID: point.ID, ToID: point.ID,
		From: point.Date, To: point.Date, FromReading: point.Odometer, ToReading: point.Odometer, Fuel: point.Fuel, Unit: point.Unit, State: reason}
}

func fuelSegmentSupported(point fuelSegmentPoint) bool {
	return (point.Fuel == "diesel" || point.Fuel == "petrol" || point.Fuel == "lpg") && point.Unit == "l" || (point.Fuel == "cng" || point.Fuel == "hydrogen") && point.Unit == "kg"
}

func fuelSegmentValidate(point fuelSegmentPoint) error {
	reading, readingErr := value.Decimal(point.Odometer)
	initial, initialErr := value.Decimal(point.Initial)
	quantity, quantityErr := value.Decimal(point.Quantity)
	cost, costOK := new(big.Int).SetString(point.CostMinor, 10)
	if !value.ValidUUID(point.ID) || !value.ValidUUID(point.VehicleID) || !value.ValidUUID(point.SeriesID) ||
		!value.Date(point.Date) || !value.Date(point.Started) || point.Date < point.Started ||
		point.Ended != nil && (!value.Date(*point.Ended) || point.Date > *point.Ended) ||
		readingErr != nil || initialErr != nil || quantityErr != nil || reading.Sign() < 0 || initial.Sign() < 0 ||
		reading.Cmp(initial) < 0 || quantity.Sign() <= 0 || point.CostMinor != "" && (!costOK || cost.Sign() < 0) ||
		point.Currency != "" && (len(point.Currency) != 3 || strings.IndexFunc(point.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0) {
		return Invalid{"Gespeicherte Tankhistorie und Zählerreihe prüfen"}
	}
	return nil
}

func fuelSegmentAccumulate(checkpoint *fuelSegmentCheckpoint, point fuelSegmentPoint) error {
	quantity, err := fuelAggregateDecimal(checkpoint.Quantity)
	if err != nil || quantity.Sign() < 0 {
		return Invalid{"Verbrauchsprojektion erneut aufbauen"}
	}
	part, _ := value.Decimal(point.Quantity)
	checkpoint.Quantity = quantity.Add(quantity, part).FloatString(6)
	if point.Currency == "" || point.CostMinor == "" {
		checkpoint.CostUnknown = true
		return nil
	}
	if _, exists := checkpoint.Costs[point.Currency]; !exists && len(checkpoint.Costs) >= fuelSegmentCurrencyLimit {
		checkpoint.CostOverflow = true
		return nil
	}
	previous := new(big.Int)
	if text := checkpoint.Costs[point.Currency]; text != "" {
		if _, ok := previous.SetString(text, 10); !ok || previous.Sign() < 0 {
			return Invalid{"Kostenprojektion erneut aufbauen"}
		}
	}
	partCost, _ := new(big.Int).SetString(point.CostMinor, 10)
	checkpoint.Costs[point.Currency] = previous.Add(previous, partCost).String()
	return nil
}

func fuelSegmentFlush(checkpoint *fuelSegmentCheckpoint) ([]fuelComputedSegment, error) {
	if checkpoint.Pending == nil {
		return nil, nil
	}
	point := *checkpoint.Pending
	checkpoint.Pending = nil
	tied := checkpoint.PendingTied
	checkpoint.PendingTied = false
	if checkpoint.SeriesID != point.SeriesID {
		checkpoint.SeriesID = point.SeriesID
		checkpoint.Previous = nil
		checkpoint.Blocked = false
		checkpoint.LastFuel = ""
		fuelSegmentReset(checkpoint, "no_full_anchor")
	}
	if err := fuelSegmentValidate(point); err != nil {
		return nil, err
	}
	if checkpoint.Blocked {
		return []fuelComputedSegment{fuelSegmentGap(point, "odometer_backwards")}, nil
	}
	if checkpoint.Previous != nil {
		prior, _ := value.Decimal(checkpoint.Previous.Odometer)
		current, _ := value.Decimal(point.Odometer)
		if point.Date < checkpoint.Previous.Date || current.Cmp(prior) < 0 {
			checkpoint.Blocked = true
			fuelSegmentReset(checkpoint, "odometer_backwards")
			return []fuelComputedSegment{fuelSegmentGap(point, "odometer_backwards")}, nil
		}
	}
	previous := point
	checkpoint.Previous = &previous
	if tied {
		fuelSegmentReset(checkpoint, "ambiguous_order")
		checkpoint.LastFuel = ""
		return []fuelComputedSegment{fuelSegmentGap(point, "ambiguous_order")}, nil
	}
	if !fuelSegmentSupported(point) {
		fuelSegmentReset(checkpoint, "unsupported_unit")
		checkpoint.LastFuel = ""
		return []fuelComputedSegment{fuelSegmentGap(point, "unsupported_unit")}, nil
	}
	segments := []fuelComputedSegment{}
	if point.Missing {
		fuelSegmentReset(checkpoint, "missing_fill")
		segments = append(segments, fuelSegmentGap(point, "missing_fill"))
	} else if checkpoint.LastFuel != "" && checkpoint.LastFuel != point.Fuel {
		fuelSegmentReset(checkpoint, "fuel_changed")
		segments = append(segments, fuelSegmentGap(point, "fuel_changed"))
	}
	checkpoint.LastFuel = point.Fuel
	if checkpoint.Anchor != nil {
		if err := fuelSegmentAccumulate(checkpoint, point); err != nil {
			return nil, err
		}
	}
	if !point.Full {
		if checkpoint.Anchor != nil {
			checkpoint.Reason = "needs_next_full"
		}
		return segments, nil
	}
	if checkpoint.Anchor != nil {
		reading, _ := value.Decimal(point.Odometer)
		anchor, _ := value.Decimal(checkpoint.Anchor.Odometer)
		distance := new(big.Rat).Sub(reading, anchor)
		if distance.Sign() > 0 {
			quantity, err := fuelAggregateDecimal(checkpoint.Quantity)
			if err != nil {
				return nil, err
			}
			rate := new(big.Rat).Mul(new(big.Rat).Quo(quantity, distance), big.NewRat(100, 1))
			quantityText, distanceText, rateText := quantity.FloatString(6), distance.FloatString(6), rate.FloatString(2)
			costs := map[string]string{}
			if !checkpoint.CostOverflow && !checkpoint.CostUnknown {
				for currency, amount := range checkpoint.Costs {
					costs[currency] = amount
				}
			}
			segments = append(segments, fuelComputedSegment{VehicleID: point.VehicleID, SeriesID: point.SeriesID,
				FromID: checkpoint.Anchor.ID, ToID: point.ID, From: checkpoint.Anchor.Date, To: point.Date,
				FromReading: checkpoint.Anchor.Odometer, ToReading: point.Odometer,
				Fuel: point.Fuel, Unit: point.Unit, State: "available", Quantity: &quantityText, Distance: &distanceText,
				Per100: &rateText, Costs: costs, CostUnknown: checkpoint.CostOverflow || checkpoint.CostUnknown,
				BelowPrecision: rate.Sign() > 0 && rate.Cmp(big.NewRat(1, 100)) < 0})
			checkpoint.Reason = "available"
		} else {
			checkpoint.Reason = "zero_distance"
			segments = append(segments, fuelSegmentGap(point, "zero_distance"))
		}
	} else if checkpoint.Reason == "no_full_anchor" {
		checkpoint.Reason = "needs_next_full"
	}
	anchor := point
	checkpoint.Anchor = &anchor
	checkpoint.Quantity = "0.000000"
	checkpoint.Costs = map[string]string{}
	checkpoint.CostOverflow = false
	checkpoint.CostUnknown = false
	return fuelUniqueSegments(segments), nil
}

func fuelUniqueSegments(segments []fuelComputedSegment) []fuelComputedSegment {
	seen := map[string]bool{}
	result := make([]fuelComputedSegment, 0, len(segments))
	for _, segment := range segments {
		key := strings.Join([]string{segment.VehicleID, segment.SeriesID, segment.FromID, segment.ToID, segment.State}, "/")
		if !seen[key] {
			seen[key] = true
			result = append(result, segment)
		}
	}
	return result
}

func fuelSegmentFeed(ctx context.Context, checkpoint *fuelSegmentCheckpoint, points []fuelSegmentPoint, finish bool) ([]fuelComputedSegment, error) {
	if checkpoint == nil || checkpoint.Algorithm != fuelSegmentAlgorithm || len(points) > fuelSegmentChunk || len(checkpoint.Costs) > fuelSegmentCurrencyLimit {
		return nil, Invalid{"Verbrauchsprojektion erneut aufbauen"}
	}
	if checkpoint.Costs == nil {
		checkpoint.Costs = map[string]string{}
	}
	segments := []fuelComputedSegment{}
	for _, point := range points {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if checkpoint.Rows == math.MaxInt64 {
			return nil, Invalid{"Tankhistorie überschreitet die Projektionsgrenze"}
		}
		if err := fuelSegmentValidate(point); err != nil {
			return nil, err
		}
		if checkpoint.Pending != nil {
			pending := checkpoint.Pending
			pendingReading, pendingErr := value.Decimal(pending.Odometer)
			reading, _ := value.Decimal(point.Odometer)
			if pendingErr != nil {
				return nil, Invalid{"Verbrauchsprojektion erneut aufbauen"}
			}
			if point.SeriesID == pending.SeriesID && point.Date == pending.Date && reading.Cmp(pendingReading) == 0 {
				checkpoint.PendingTied = true
			} else {
				if checkpoint.SeriesID != "" && pending.SeriesID != checkpoint.SeriesID && checkpoint.Previous != nil && checkpoint.Reason != "available" && !checkpoint.Blocked {
					segments = append(segments, fuelSegmentGap(*checkpoint.Previous, checkpoint.Reason))
				}
				completed, err := fuelSegmentFlush(checkpoint)
				if err != nil {
					return nil, err
				}
				segments = append(segments, completed...)
				copy := point
				checkpoint.Pending = &copy
			}
		} else {
			copy := point
			checkpoint.Pending = &copy
		}
		checkpoint.Rows++
		checkpoint.CursorSeries, checkpoint.CursorDay, checkpoint.CursorOdometer, checkpoint.CursorID = point.SeriesID, point.Date, point.Odometer, point.ID
	}
	if finish {
		if checkpoint.Pending != nil && checkpoint.SeriesID != "" && checkpoint.Pending.SeriesID != checkpoint.SeriesID && checkpoint.Previous != nil && checkpoint.Reason != "available" && !checkpoint.Blocked {
			segments = append(segments, fuelSegmentGap(*checkpoint.Previous, checkpoint.Reason))
		}
		completed, err := fuelSegmentFlush(checkpoint)
		if err != nil {
			return nil, err
		}
		segments = append(segments, completed...)
		if checkpoint.Previous != nil && checkpoint.Reason != "available" && !checkpoint.Blocked {
			segments = append(segments, fuelSegmentGap(*checkpoint.Previous, checkpoint.Reason))
		}
	}
	return fuelUniqueSegments(segments), nil
}

// Aggregates have a separate bound from a single input value. Reapplying the
// twelve-digit input parser to an accumulated quantity would reject valid large
// histories and could turn their result into a partial total.
func fuelAggregateDecimal(text string) (*big.Rat, error) {
	if len(text) == 0 || len(text) > 64 {
		return nil, errors.New("invalid Fuel aggregate")
	}
	whole, fraction, hasFraction := strings.Cut(text, ".")
	whole = strings.TrimPrefix(whole, "-")
	if whole == "" || len(whole) > 48 || strings.IndexFunc(whole, func(r rune) bool { return r < '0' || r > '9' }) >= 0 ||
		hasFraction && (fraction == "" || len(fraction) > 6 || strings.IndexFunc(fraction, func(r rune) bool { return r < '0' || r > '9' }) >= 0) {
		return nil, errors.New("invalid Fuel aggregate")
	}
	result, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, errors.New("invalid Fuel aggregate")
	}
	return result, nil
}

func fuelSegmentRecord(segment fuelComputedSegment) Record {
	return Record{"vehicle_id": segment.VehicleID, "series_id": segment.SeriesID, "from_id": segment.FromID, "to_id": segment.ToID,
		"from_on": segment.From, "to_on": segment.To, "fuel_type": segment.Fuel, "unit": segment.Unit,
		"from_reading": segment.FromReading, "to_reading": segment.ToReading,
		"state": segment.State, "quantity": fuelNullableText(segment.Quantity), "distance": fuelNullableText(segment.Distance), "per_100": fuelNullableText(segment.Per100),
		"costs_minor": segment.Costs, "cost_unknown": segment.CostUnknown, "below_precision": segment.BelowPrecision}
}

func fuelNullableText(text *string) any {
	if text == nil {
		return nil
	}
	return *text
}

func fuelSegmentJSON(checkpoint fuelSegmentCheckpoint) ([]byte, error) {
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64<<10 {
		return nil, Invalid{"Verbrauchsprojektion überschreitet die Zustandsgrenze"}
	}
	return encoded, nil
}

func fuelSegmentID(household string, version int64, segment fuelComputedSegment) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{household, segment.VehicleID, strconv.FormatInt(version, 10),
		strconv.Itoa(fuelSegmentAlgorithm), segment.SeriesID, segment.FromID, segment.ToID, segment.State}, "/")))
	hash[6] = (hash[6] & 0x0f) | 0x50
	hash[8] = (hash[8] & 0x3f) | 0x80
	text := hex.EncodeToString(hash[:16])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

func fuelCheckpointDecode(raw []byte, vehicle string) (fuelSegmentCheckpoint, error) {
	checkpoint := fuelEmptyCheckpoint()
	if len(raw) == 0 || bytes.Equal(raw, []byte("{}")) {
		return checkpoint, nil
	}
	if len(raw) > 64<<10 {
		return checkpoint, Invalid{"Verbrauchsprojektion erneut aufbauen"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&checkpoint); err != nil {
		return checkpoint, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return checkpoint, Invalid{"Verbrauchsprojektion erneut aufbauen"}
	}
	if checkpoint.Algorithm != fuelSegmentAlgorithm || checkpoint.Rows < 0 || len(checkpoint.Costs) > fuelSegmentCurrencyLimit || !value.Date(checkpoint.AsOf) {
		return checkpoint, Invalid{"Verbrauchsprojektion erneut aufbauen"}
	}
	for _, point := range []*fuelSegmentPoint{checkpoint.Pending, checkpoint.Anchor, checkpoint.Previous} {
		if point != nil {
			if point.VehicleID != vehicle {
				return checkpoint, Invalid{"Projektionszustand gehört nicht zum Fahrzeug"}
			}
			if err := fuelSegmentValidate(*point); err != nil {
				return checkpoint, err
			}
		}
	}
	return checkpoint, nil
}

func fuelSegmentScanRows(rows pgx.Rows) ([]fuelSegmentPoint, error) {
	defer rows.Close()
	points := []fuelSegmentPoint{}
	for rows.Next() {
		var point fuelSegmentPoint
		if err := rows.Scan(&point.ID, &point.VehicleID, &point.SeriesID, &point.Date, &point.Odometer, &point.Quantity,
			&point.Fuel, &point.Unit, &point.Currency, &point.CostMinor, &point.Initial, &point.Started, &point.Ended,
			&point.Full, &point.Missing, &point.Version); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

const fuelSegmentPointProjection = `f.id,f.vehicle_id,f.odometer_series_id,f.occurred_on::text,f.odometer::text,f.quantity::text,
 f.fuel_type,f.quantity_unit,f.currency_code,f.total_minor::text,s.initial_reading::text,s.started_on::text,s.ended_on::text,
 f.full_tank,f.previous_fill_missing,f.version`

func fuelSegmentChunkRows(ctx context.Context, tx pgx.Tx, household, vehicle string, checkpoint fuelSegmentCheckpoint) ([]fuelSegmentPoint, error) {
	var series, day, reading, pointID any
	if checkpoint.CursorID != "" {
		if !value.ValidUUID(checkpoint.CursorSeries) || !value.ValidUUID(checkpoint.CursorID) || !value.Date(checkpoint.CursorDay) {
			return nil, Invalid{"Projektionsfortsetzung erneut aufbauen"}
		}
		if _, err := value.Decimal(checkpoint.CursorOdometer); err != nil {
			return nil, err
		}
		series, day, reading, pointID = checkpoint.CursorSeries, checkpoint.CursorDay, checkpoint.CursorOdometer, checkpoint.CursorID
	}
	rows, err := tx.Query(ctx, `SELECT `+fuelSegmentPointProjection+` FROM fuel.fillups f JOIN fuel.odometer_series s
 ON s.household_id=f.household_id AND s.vehicle_id=f.vehicle_id AND s.id=f.odometer_series_id
 WHERE f.household_id=$1 AND f.vehicle_id=$2 AND f.occurred_on<=$7::date AND($3::uuid IS NULL OR
 (f.odometer_series_id,f.occurred_on,f.odometer,f.id)>($3::uuid,$4::date,$5::numeric,$6::uuid))
 ORDER BY f.odometer_series_id,f.occurred_on,f.odometer,f.id LIMIT 500`, household, vehicle, series, day, reading, pointID, checkpoint.AsOf)
	if err != nil {
		return nil, err
	}
	return fuelSegmentScanRows(rows)
}

func fuelSegmentPersist(ctx context.Context, tx pgx.Tx, household string, version int64, segments []fuelComputedSegment) error {
	for _, segment := range segments {
		costValues := segment.Costs
		if costValues == nil {
			costValues = map[string]string{}
		}
		costs, err := json.Marshal(costValues)
		if err != nil {
			return err
		}
		state, reason := "unknown", segment.State
		if segment.State == "available" {
			state, reason = "known", "complete_full_interval"
		}
		_, err = tx.Exec(ctx, `INSERT INTO fuel.consumption_segments(id,household_id,vehicle_id,odometer_series_id,source_version,algorithm_version,
 from_fillup_id,to_fillup_id,from_on,to_on,from_odometer,to_odometer,quantity,distance,quantity_unit,fuel_type,state,reason,below_precision,costs_minor)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif($11,'')::numeric,nullif($12,'')::numeric,$13,$14,$15,$16,$17,$18,$19,$20)
 ON CONFLICT(id) DO UPDATE SET costs_minor=excluded.costs_minor,quantity=excluded.quantity,distance=excluded.distance,
 state=excluded.state,reason=excluded.reason,below_precision=excluded.below_precision`,
			fuelSegmentID(household, version, segment), household, segment.VehicleID, segment.SeriesID, version, fuelSegmentAlgorithm,
			segment.FromID, segment.ToID, segment.From, segment.To, segment.FromReading, segment.ToReading,
			segment.Quantity, segment.Distance, segment.Unit, segment.Fuel, state, reason, segment.BelowPrecision, costs)
		if err != nil {
			return err
		}
	}
	return nil
}

// Housekeeping advances bounded private projections only. It never invents an
// actor session, performs a business mutation, or publishes a partial version.
func (s *Store) FuelModuleHousekeeping(ctx context.Context, limit int) error {
	if limit <= 0 || limit > 8 {
		limit = 1
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `WITH due AS(SELECT state.household_id,state.vehicle_id FROM fuel.segment_state state
 JOIN core.households h ON h.id=state.household_id JOIN core.module_states m ON m.household_id=h.id AND m.module_id='fuel' AND m.state='enabled'
 CROSS JOIN LATERAL(SELECT CASE WHEN pg_input_is_valid(state.checkpoint->>'as_of','date') THEN(state.checkpoint->>'as_of')::date END AS day) cutoff
 WHERE state.status='ready' AND(cutoff.day IS NULL OR cutoff.day>(transaction_timestamp() AT TIME ZONE h.timezone)::date
 OR EXISTS(SELECT 1 FROM fuel.fillups f WHERE f.household_id=state.household_id AND f.vehicle_id=state.vehicle_id
 AND f.occurred_on>cutoff.day AND f.occurred_on<=(transaction_timestamp() AT TIME ZONE h.timezone)::date))
 AND(SELECT count(*)=1 AND bool_and(version IN(26,27) AND NOT dirty) FROM core.schema_migrations)
 ORDER BY state.updated_at LIMIT $1 FOR UPDATE OF state SKIP LOCKED), reset AS(
 UPDATE fuel.segment_state state SET source_version=source_version+1,status='pending',checkpoint='{}',cursor_series=NULL,cursor_day=NULL,
 cursor_odometer=NULL,cursor_id=NULL,updated_at=transaction_timestamp() FROM due
 WHERE state.household_id=due.household_id AND state.vehicle_id=due.vehicle_id RETURNING state.household_id,state.vehicle_id,state.source_version)
 INSERT INTO fuel.segment_jobs(household_id,vehicle_id,target_version) SELECT household_id,vehicle_id,source_version FROM reset
 ON CONFLICT(household_id,vehicle_id) DO UPDATE SET target_version=excluded.target_version,state='pending',attempts=0,lease_until=NULL,
 next_attempt_at=transaction_timestamp(),updated_at=transaction_timestamp()`, limit)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `WITH stale AS(SELECT state.household_id,state.vehicle_id,state.source_version
 FROM fuel.segment_state state JOIN core.module_states m ON m.household_id=state.household_id AND m.module_id='fuel' AND m.state='enabled'
 WHERE(state.algorithm_version<>$1 OR state.built_version<>state.source_version)
 AND NOT EXISTS(SELECT 1 FROM fuel.segment_jobs j WHERE j.household_id=state.household_id AND j.vehicle_id=state.vehicle_id)
 AND(SELECT count(*)=1 AND bool_and(version IN(26,27) AND NOT dirty) FROM core.schema_migrations)
 ORDER BY state.updated_at,state.household_id,state.vehicle_id LIMIT $2)
 INSERT INTO fuel.segment_jobs(household_id,vehicle_id,target_version) SELECT household_id,vehicle_id,source_version FROM stale
 ON CONFLICT(household_id,vehicle_id) DO NOTHING`, fuelSegmentAlgorithm, limit)
	if err != nil {
		return err
	}
	for range limit {
		worked, err := s.fuelSegmentWork(ctx)
		if err != nil || !worked {
			return err
		}
	}
	return nil
}

func (s *Store) fuelSegmentWork(ctx context.Context) (worked bool, workErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	var household, vehicle string
	var target int64
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
		if workErr != nil && household != "" && vehicle != "" {
			// A malformed first job must not starve every later vehicle. Match
			// the failed revision so a new invalidation is never penalized.
			_, _ = s.Pool.Exec(cleanup, `UPDATE fuel.segment_jobs SET state='failed',attempts=least(attempts+1,1000),lease_until=NULL,
 next_attempt_at=transaction_timestamp()+make_interval(secs=>least(attempts+1,60)*30),updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND target_version=$3`, household, vehicle, target)
			_, _ = s.Pool.Exec(cleanup, `UPDATE fuel.segment_state SET status='failed',updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND source_version=$3 AND status<>'ready'`, household, vehicle, target)
		}
	}()
	err = tx.QueryRow(ctx, `SELECT j.household_id,j.vehicle_id,j.target_version FROM fuel.segment_jobs j
 JOIN core.module_states m ON m.household_id=j.household_id AND m.module_id='fuel' AND m.state='enabled'
 WHERE j.next_attempt_at<=transaction_timestamp() AND(j.lease_until IS NULL OR j.lease_until<transaction_timestamp())
 AND(SELECT count(*)=1 AND bool_and(version IN(26,27) AND NOT dirty) FROM core.schema_migrations)
 ORDER BY j.next_attempt_at,j.household_id,j.vehicle_id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`).Scan(&household, &vehicle, &target)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var today string
	if err = tx.QueryRow(ctx, "SELECT (transaction_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1 FOR SHARE NOWAIT", household).Scan(&today); err != nil {
		return false, err
	}
	var generation int64
	if err = tx.QueryRow(ctx, "SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id='fuel' AND state='enabled' FOR SHARE NOWAIT", household).Scan(&generation); err != nil {
		return false, err
	}
	if generation <= 0 {
		return false, ErrNotFound
	}
	var source int64
	var previousAlgorithm int
	var status string
	var checkpointJSON []byte
	err = tx.QueryRow(ctx, `SELECT source_version,status,checkpoint,algorithm_version FROM fuel.segment_state
	WHERE household_id=$1 AND vehicle_id=$2 FOR UPDATE NOWAIT`, household, vehicle).Scan(&source, &status, &checkpointJSON, &previousAlgorithm)
	if err != nil {
		return false, err
	}
	if source != target {
		_, err = tx.Exec(ctx, `UPDATE fuel.segment_jobs SET target_version=$3,state='pending',lease_until=NULL,next_attempt_at=transaction_timestamp(),updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2`, household, vehicle, source)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	checkpoint := fuelEmptyCheckpoint()
	checkpoint.AsOf = today
	if status != "pending" && previousAlgorithm == fuelSegmentAlgorithm {
		checkpoint, err = fuelCheckpointDecode(checkpointJSON, vehicle)
		if err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE fuel.segment_jobs SET state='running',lease_until=transaction_timestamp()+interval '30 seconds',updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND target_version=$3`, household, vehicle, target); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE fuel.segment_state SET status='building',algorithm_version=$3,updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2`, household, vehicle, fuelSegmentAlgorithm); err != nil {
		return false, err
	}
	points, err := fuelSegmentChunkRows(ctx, tx, household, vehicle, checkpoint)
	if err != nil {
		return false, err
	}
	finished := len(points) < fuelSegmentChunk
	segments, err := fuelSegmentFeed(ctx, &checkpoint, points, finished)
	if err != nil {
		return false, err
	}
	if err = fuelSegmentPersist(ctx, tx, household, target, segments); err != nil {
		return false, err
	}
	encoded, err := fuelSegmentJSON(checkpoint)
	if err != nil {
		return false, err
	}
	state := "building"
	if finished {
		state = "ready"
	}
	var cursorSeries, cursorDay, cursorReading, cursorID any
	if checkpoint.CursorID != "" {
		cursorSeries, cursorDay, cursorReading, cursorID = checkpoint.CursorSeries, checkpoint.CursorDay, checkpoint.CursorOdometer, checkpoint.CursorID
	}
	updated, err := tx.Exec(ctx, `UPDATE fuel.segment_state SET algorithm_version=$4,checkpoint=$5,cursor_series=$6,cursor_day=$7,cursor_odometer=$8,cursor_id=$9,
 status=$10,built_version=CASE WHEN $10='ready' THEN $3 ELSE built_version END,updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND source_version=$3`, household, vehicle, target, fuelSegmentAlgorithm, encoded,
		cursorSeries, cursorDay, cursorReading, cursorID, state)
	if err != nil {
		return false, err
	}
	if updated.RowsAffected() != 1 {
		return false, ErrConflict
	}
	if finished {
		_, err = tx.Exec(ctx, "DELETE FROM fuel.segment_jobs WHERE household_id=$1 AND vehicle_id=$2 AND target_version=$3", household, vehicle, target)
	} else {
		_, err = tx.Exec(ctx, `UPDATE fuel.segment_jobs SET state='pending',lease_until=NULL,attempts=0,next_attempt_at=transaction_timestamp(),updated_at=transaction_timestamp()
 WHERE household_id=$1 AND vehicle_id=$2 AND target_version=$3`, household, vehicle, target)
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `WITH old AS(SELECT id FROM fuel.consumption_segments WHERE household_id=$1 AND vehicle_id=$2
 AND(source_version<>$3 OR algorithm_version<>$4) ORDER BY id LIMIT 500)
 DELETE FROM fuel.consumption_segments cache USING old WHERE cache.id=old.id`, household, vehicle, target, fuelSegmentAlgorithm)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func fuelSegmentCacheVersion(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, vehicle string) (int64, string, error) {
	var source, built int64
	var algorithm int
	var status string
	var cutoff *string
	err := tx.QueryRow(ctx, `SELECT source_version,built_version,algorithm_version,status,checkpoint->>'as_of'
 FROM fuel.segment_state WHERE household_id=$1 AND vehicle_id=$2`, id.HouseholdID, vehicle).Scan(&source, &built, &algorithm, &status, &cutoff)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "segments_pending", nil
	}
	if err != nil {
		return 0, "", err
	}
	if status == "failed" {
		return 0, "segments_failed", nil
	}
	if status != "ready" || source != built || algorithm != fuelSegmentAlgorithm || cutoff == nil || !value.Date(*cutoff) || *cutoff > scope.Today {
		return 0, "segments_pending", nil
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM fuel.fillups WHERE household_id=$1 AND vehicle_id=$2
 AND occurred_on>$3::date AND occurred_on<=$4::date)`, id.HouseholdID, vehicle, *cutoff, scope.Today).Scan(&eligible)
	if err != nil {
		return 0, "", err
	}
	if eligible {
		return 0, "segments_pending", nil
	}
	return source, "ready", nil
}

func fuelWindowSegments(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery) ([]fuelComputedSegment, string, error) {
	var seriesCount int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM(SELECT id FROM fuel.odometer_series WHERE household_id=$1 AND vehicle_id=$2
 AND started_on<=$4::date AND(ended_on IS NULL OR ended_on>=$3::date) LIMIT 1001) bounded`, id.HouseholdID, query.VehicleID, query.From, query.To).Scan(&seriesCount)
	if err != nil {
		return nil, "", err
	}
	if seriesCount > 1000 {
		return nil, "series_limit", nil
	}
	// Verify the complete bounded series prefix: restarting at a later full
	// receipt could conceal an earlier backwards counter in the same series.
	// Large prefixes wait for the chunked cache instead of assuming validity.
	// Include the next real full closing boundary and all of its tied receipts.
	rows, err := tx.Query(ctx, `WITH bounds AS(
 SELECT s.id,right_edge.occurred_on AS right_day,right_edge.odometer AS right_reading
 FROM fuel.odometer_series s
 LEFT JOIN LATERAL(SELECT occurred_on,odometer FROM fuel.fillups f WHERE f.household_id=s.household_id
 AND f.odometer_series_id=s.id AND f.occurred_on>$4::date AND f.occurred_on<=$5::date AND f.full_tank
 ORDER BY occurred_on,odometer,id LIMIT 1) right_edge ON true
 WHERE s.household_id=$1 AND s.vehicle_id=$2 AND s.started_on<=$4::date AND(s.ended_on IS NULL OR s.ended_on>=$3::date))
 SELECT `+fuelSegmentPointProjection+` FROM bounds b JOIN fuel.fillups f ON f.odometer_series_id=b.id AND f.household_id=$1 AND f.vehicle_id=$2
 JOIN fuel.odometer_series s ON s.household_id=f.household_id AND s.vehicle_id=f.vehicle_id AND s.id=f.odometer_series_id
 WHERE f.occurred_on<=$5::date
 AND((b.right_day IS NULL AND f.occurred_on<=$4::date) OR(f.occurred_on,f.odometer)<=(b.right_day,b.right_reading))
 ORDER BY f.odometer_series_id,f.occurred_on,f.odometer,f.id LIMIT 10001`, id.HouseholdID, query.VehicleID, query.From, query.To, scope.Today)
	if err != nil {
		return nil, "", err
	}
	points, err := fuelSegmentScanRows(rows)
	if err != nil {
		return nil, "", err
	}
	if len(points) > 10000 {
		return nil, "history_window_limit", nil
	}
	checkpoint := fuelEmptyCheckpoint()
	segments := []fuelComputedSegment{}
	for start := 0; start < len(points); start += fuelSegmentChunk {
		end := min(start+fuelSegmentChunk, len(points))
		completed, feedErr := fuelSegmentFeed(ctx, &checkpoint, points[start:end], end == len(points))
		if feedErr != nil {
			return nil, "", feedErr
		}
		for _, segment := range completed {
			if segment.To >= query.From && segment.From <= query.To && (query.Unit == "" || segment.Unit == query.Unit) {
				segments = append(segments, segment)
			}
		}
	}
	return fuelUniqueSegments(segments), "live_complete_window", nil
}

func fuelOverviewCachedInterval(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicle, series, today string, latest []FuelOverviewFill) (*FuelOverviewInterval, string, error) {
	rows, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('known',CASE WHEN closed_segment.id IS NULL THEN NULL ELSE
 jsonb_build_object('from_id',closed_segment.from_fillup_id,'to_id',closed_segment.to_fillup_id,'from_on',closed_segment.from_on,'to_on',closed_segment.to_on,
 'to_odometer',closed_segment.to_odometer::text,'quantity',closed_segment.quantity::text,'distance',closed_segment.distance::text,
 'unit',closed_segment.quantity_unit,'fuel_type',closed_segment.fuel_type,'below_precision',closed_segment.below_precision) END,
 'gap',CASE WHEN gap.id IS NULL THEN NULL ELSE jsonb_build_object('reason',gap.reason,'to_on',gap.to_on,'to_odometer',gap.to_odometer::text) END)
 FROM fuel.segment_state state
 CROSS JOIN LATERAL(SELECT CASE WHEN pg_input_is_valid(state.checkpoint->>'as_of','date') THEN(state.checkpoint->>'as_of')::date END AS day) cutoff
 LEFT JOIN LATERAL(SELECT cache.* FROM fuel.consumption_segments cache WHERE cache.household_id=state.household_id AND cache.vehicle_id=state.vehicle_id
 AND cache.source_version=state.source_version AND cache.algorithm_version=$4 AND cache.odometer_series_id=$3 AND cache.state='known'
 AND cache.to_on<=$5::date ORDER BY cache.to_on DESC,cache.to_odometer DESC,cache.id DESC LIMIT 1) closed_segment ON true
 LEFT JOIN LATERAL(SELECT cache.id,cache.reason,cache.to_on,cache.to_odometer FROM fuel.consumption_segments cache WHERE cache.household_id=state.household_id
 AND cache.vehicle_id=state.vehicle_id AND cache.source_version=state.source_version AND cache.algorithm_version=$4
 AND cache.odometer_series_id=$3 AND cache.state='unknown' AND cache.to_on<=$5::date
 ORDER BY cache.to_on DESC,cache.to_odometer DESC,cache.id DESC LIMIT 1) gap ON true
 WHERE state.household_id=$1 AND state.vehicle_id=$2 AND state.status='ready' AND state.algorithm_version=$4 AND state.built_version=state.source_version
 AND cutoff.day IS NOT NULL AND cutoff.day<=$5::date AND NOT EXISTS(SELECT 1 FROM fuel.fillups f WHERE f.household_id=state.household_id
 AND f.vehicle_id=state.vehicle_id AND f.occurred_on>cutoff.day AND f.occurred_on<=$5::date)`, id.HouseholdID, vehicle, series, fuelSegmentAlgorithm, today)
	if err != nil {
		return nil, "", err
	}
	if len(rows) == 0 {
		return nil, "segments_pending", nil
	}
	known, hasKnown := rows[0]["known"].(map[string]any)
	gap, hasGap := rows[0]["gap"].(map[string]any)
	if !hasKnown {
		if hasGap {
			return nil, String(gap["reason"]), nil
		}
		return nil, "no_fillups", nil
	}
	quantity, qErr := fuelAggregateDecimal(String(known["quantity"]))
	distance, dErr := fuelAggregateDecimal(String(known["distance"]))
	if qErr != nil || dErr != nil || quantity.Sign() <= 0 || distance.Sign() <= 0 {
		return nil, "", Invalid{"Verbrauchsprojektion erneut aufbauen"}
	}
	rate := new(big.Rat).Mul(new(big.Rat).Quo(quantity, distance), big.NewRat(100, 1))
	interval := &FuelOverviewInterval{From: String(known["from_on"]), To: String(known["to_on"]), FromID: String(known["from_id"]), ToID: String(known["to_id"]),
		Quantity: quantity.FloatString(6), Distance: distance.FloatString(6), Per100: rate.FloatString(2), Unit: String(known["unit"]) + "/100 km", Fuel: String(known["fuel_type"]),
		BelowPrecision: rate.Sign() > 0 && rate.Cmp(big.NewRat(1, 100)) < 0}
	state := "available"
	if len(latest) > 0 {
		interval.Historical = latest[0].ID != interval.ToID
		if interval.Historical {
			state = "needs_next_full"
		}
	}
	if hasGap && String(gap["to_on"]) >= interval.To {
		later := String(gap["to_on"]) > interval.To
		if !later {
			gapReading, gapErr := value.Decimal(String(gap["to_odometer"]))
			closedReading, closeErr := value.Decimal(String(known["to_odometer"]))
			later = gapErr == nil && closeErr == nil && gapReading.Cmp(closedReading) >= 0
		}
		if later {
			state = String(gap["reason"])
		}
	}
	return interval, state, nil
}
