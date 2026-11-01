//go:build integration

package web

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"household/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type choreReadinessTrace struct {
	started atomic.Int32
	active  atomic.Int32
	maximum atomic.Int32
	release chan struct{}
	once    sync.Once
}

type choreReadinessTraceKey struct{}

func (t *choreReadinessTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.Contains(data.SQL, "_ready()") {
		return ctx
	}
	active := t.active.Add(1)
	for previous := t.maximum.Load(); active > previous; previous = t.maximum.Load() {
		if t.maximum.CompareAndSwap(previous, active) {
			break
		}
	}
	started := t.started.Add(1)
	if started == 3 {
		t.once.Do(func() { close(t.release) })
	}
	if started <= 3 {
		select {
		case <-t.release:
		case <-ctx.Done():
		}
	}
	return context.WithValue(ctx, choreReadinessTraceKey{}, true)
}

func (t *choreReadinessTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if traced, _ := ctx.Value(choreReadinessTraceKey{}).(bool); traced {
		t.active.Add(-1)
	}
}

func TestChoreCurrentReadinessBoundsParallelInspectionAndCancellation(t *testing.T) {
	f := newIntegrationFixture(t)
	config, err := pgxpool.ParseConfig(os.Getenv("HOUSEHOLD_TEST_APP_URL"))
	if err != nil {
		t.Fatal("invalid isolated App fixture configuration")
	}
	trace := &choreReadinessTrace{release: make(chan struct{})}
	config.MaxConns = 6
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(f.ctx, config)
	if err != nil {
		t.Fatal("open isolated instrumented App pool")
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	if err = database.Ready(ctx, pool); err != nil {
		t.Fatal("current full contract inspection failed", err)
	}
	if trace.started.Load() < 20 || trace.maximum.Load() != 3 || trace.active.Load() != 0 {
		t.Fatal("not all current contracts were inspected with exactly three bounded lanes and no remaining query", trace.started.Load(), trace.maximum.Load(), trace.active.Load())
	}
	canceled, stop := context.WithCancel(f.ctx)
	stop()
	if err = database.Ready(canceled, pool); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation identity lost", err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("readiness cancellation retained a pool connection")
	}
}
