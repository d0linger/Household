package worker

import (
	"context"
	"errors"
	"time"

	"household/internal/meterfeed"
	"household/internal/modules"
	"household/internal/store"

	"github.com/jackc/pgx/v5/pgconn"
)

func (w *Worker) energyAdapterLoop(ctx context.Context) {
	for ctx.Err() == nil {
		var handled bool
		err := guarded(ctx, func(ctx context.Context) (err error) {
			handled, err = w.energyAdapterOnce(ctx)
			return err
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil && w.Log != nil {
			// Neither remote bodies nor database error details expose measurements or credentials.
			attrs := []any{"reason", failureReason(err)}
			var caught panicked
			if errors.As(err, &caught) {
				attrs = append(attrs, "panic_type", caught.kind, "call_path", caught.path)
			}
			w.Log.Error("meter adapter processing failed", attrs...)
		}
		if handled && err == nil {
			continue
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// failureReason names an adapter failure class without echoing remote bodies,
// URLs or values. Feed errors are recorded on the source itself; failures that
// reach the log come from the lease's database work, deadlines or panics.
func failureReason(err error) string {
	var caught panicked
	var database *pgconn.PgError
	var connect *pgconn.ConnectError
	switch {
	case errors.As(err, &caught):
		return "panic"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, store.ErrConflict):
		return "lease_lost"
	case errors.Is(err, store.ErrForbidden):
		return "lease_refused"
	case errors.As(err, &connect):
		return "database_unreachable"
	case errors.As(err, &database):
		return "database_" + database.Code
	default:
		return "unclassified"
	}
}

func (w *Worker) energyAdapterOnce(ctx context.Context) (bool, error) {
	s := store.New(w.Pool, modules.Registry())
	lease, err := s.ClaimEnergyAdapter(ctx)
	if err != nil || lease == nil {
		return false, err
	}
	code := "ready"
	stats := meterfeed.MQTTStats{}
	switch lease.Adapter {
	case "home_assistant":
		var sample meterfeed.Sample
		sample, err = meterfeed.FetchHA(ctx, w.Cfg.MeterFeeds, lease.Source)
		if err == nil {
			var inserted bool
			inserted, err = s.IngestEnergySample(ctx, *lease, sample)
			if err == nil {
				if inserted {
					stats.Inserted = 1
				} else {
					stats.Duplicates = 1
				}
			}
		}
	case "mqtt":
		stats, err = meterfeed.ConsumeMQTT(ctx, w.Cfg.MeterFeeds, lease.Source,
			func(ctx context.Context, sample meterfeed.Sample) (bool, error) {
				inserted, err := s.IngestEnergySample(ctx, *lease, sample)
				var invalid store.Invalid
				if errors.As(err, &invalid) {
					return false, meterfeed.ErrMessage
				}
				return inserted, err
			},
			func(ctx context.Context, digest string) error {
				return s.RejectEnergySample(ctx, *lease, digest)
			})
	default:
		err = meterfeed.ErrConfiguration
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		var invalid store.Invalid
		switch {
		case errors.Is(err, store.ErrEnergyFeedRate):
			code = "backoff"
		case errors.Is(err, meterfeed.ErrConfiguration):
			code = "configuration_missing"
		case errors.Is(err, meterfeed.ErrMessage) || errors.As(err, &invalid):
			code = "invalid_message"
		default:
			code = "unavailable"
		}
	}
	return true, s.FinishEnergyAdapter(ctx, *lease, code, stats)
}
