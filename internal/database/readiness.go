package database

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type readinessCheck struct {
	query      string
	inspect    string
	incomplete string
	columns    int
}

// Contract inspectors are independent, read-only queries. Keep every check
// current while bounding their concurrency and honoring the caller's deadline.
func inspectReadiness(ctx context.Context, pool *pgxpool.Pool, checks []readinessCheck) error {
	work := make(chan readinessCheck, len(checks))
	for _, check := range checks {
		work <- check
	}
	close(work)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	failures := make(chan error, 1)
	var workers sync.WaitGroup
	for range 3 {
		workers.Go(func() {
			for check := range work {
				if run.Err() != nil {
					return
				}
				values := make([]bool, check.columns)
				targets := make([]any, len(values))
				for i := range values {
					targets[i] = &values[i]
				}
				err := pool.QueryRow(run, check.query).Scan(targets...)
				if err != nil {
					err = fmt.Errorf(check.inspect, err)
				} else {
					for _, ready := range values {
						if !ready {
							err = errors.New(check.incomplete)
							break
						}
					}
				}
				if err != nil {
					select {
					case failures <- err:
						cancel()
					default:
					}
					return
				}
			}
		})
	}
	workers.Wait()
	select {
	case err := <-failures:
		return err
	default:
		return ctx.Err()
	}
}
