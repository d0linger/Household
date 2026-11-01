package store

import (
	"context"
	"time"

	"household/internal/energy"

	"github.com/jackc/pgx/v5"
)

func energyRebuildAggregates(ctx context.Context, tx pgx.Tx, household string, dirty Record) error {
	register, zone := String(dirty["register_id"]), String(dirty["timezone"])
	cursor, err := energyTime(String(dirty["cursor_at"]))
	if err != nil {
		return err
	}
	through, err := energyTime(String(dirty["through_at"]))
	if err != nil {
		return err
	}
	for count := 0; count < 48 && cursor.Before(through); count++ {
		from, end, err := energy.BucketSpan(cursor, zone, "hour")
		if err != nil {
			return err
		}
		hour, err := energyObject(ctx, tx, `WITH portions AS(
 SELECT i.quantity,i.quality,i.from_at,i.through_at,greatest(i.from_at,$3::timestamptz) a,least(i.through_at,$4::timestamptz) b
 FROM meters.intervals i WHERE household_id=$1 AND register_id=$2 AND i.from_at<$4 AND i.through_at>$3),
 totals AS(SELECT sum(quantity*extract(epoch FROM b-a)/extract(epoch FROM through_at-from_at)) quantity,
 coalesce(floor(sum(extract(epoch FROM b-a)) FILTER(WHERE quantity IS NOT NULL)),0)::bigint covered,
 coalesce(floor(sum(extract(epoch FROM b-a)) FILTER(WHERE quantity IS NOT NULL AND(quality<>'actual' OR from_at<$3 OR through_at>$4))),0)::bigint estimated FROM portions)
 INSERT INTO meters.aggregates AS x(household_id,register_id,bucket_kind,from_at,through_at,quantity,covered_seconds,estimated_seconds,source_revision,created_by)
 SELECT $1,$2,'hour',$3,$4,quantity,covered,estimated,$5,$6 FROM totals
 ON CONFLICT(register_id,bucket_kind,from_at) DO UPDATE SET quantity=EXCLUDED.quantity,through_at=EXCLUDED.through_at,
 covered_seconds=EXCLUDED.covered_seconds,estimated_seconds=EXCLUDED.estimated_seconds,source_revision=EXCLUDED.source_revision
 RETURNING jsonb_build_object('covered',x.covered_seconds,'estimated',x.estimated_seconds)`,
			household, register, from, end, Int(dirty["source_revision"]), dirty["created_by"])
		if err != nil {
			return err
		}
		if Int(hour["covered"]) > int64(end.Sub(from)/time.Second) || Int(hour["estimated"]) > Int(hour["covered"]) {
			return Invalid{"Überlappende Messintervalle verhindern eine eindeutige Zeitaggregation"}
		}
		for _, kind := range []string{"day", "month"} {
			start, finish, err := energy.BucketSpan(from, zone, kind)
			if err != nil {
				return err
			}
			inputKind := "hour"
			if kind == "month" {
				inputKind = "day"
			}
			_, err = tx.Exec(ctx, `INSERT INTO meters.aggregates(household_id,register_id,bucket_kind,from_at,through_at,quantity,
 covered_seconds,estimated_seconds,source_revision,created_by)
 SELECT $1,$2,$3,$4,$5,sum(quantity),coalesce(sum(covered_seconds),0),coalesce(sum(estimated_seconds),0),$6,$7
 FROM meters.aggregates WHERE household_id=$1 AND register_id=$2 AND bucket_kind=$8 AND from_at>=$4 AND through_at<=$5
 ON CONFLICT(register_id,bucket_kind,from_at) DO UPDATE SET quantity=EXCLUDED.quantity,through_at=EXCLUDED.through_at,
 covered_seconds=EXCLUDED.covered_seconds,estimated_seconds=EXCLUDED.estimated_seconds,source_revision=EXCLUDED.source_revision`,
				household, register, kind, start, finish, Int(dirty["source_revision"]), dirty["created_by"], inputKind)
			if err != nil {
				return err
			}
		}
		cursor = end
	}
	if !cursor.Before(through) {
		_, err = tx.Exec(ctx, `DELETE FROM meters.dirty_ranges WHERE household_id=$1 AND register_id=$2`, household, register)
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE meters.dirty_ranges SET cursor_at=$3,cursor_id='',updated_at=clock_timestamp()
 WHERE household_id=$1 AND register_id=$2 AND phase='aggregates'`, household, register, cursor)
	return err
}
