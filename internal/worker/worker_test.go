package worker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRunCanceledBeforeStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// A stopped worker must not start another batch or require a live pool.
	directory := &fakeDirectory{remaining: 1}
	worker := &Worker{scan: directoryScan{directory: directory}}
	worker.Run(ctx)
	if directory.closed != 1 {
		t.Fatalf("worker shutdown did not close its directory cursor: %d closes", directory.closed)
	}
	if err := worker.Close(); err != nil || directory.closed != 1 {
		t.Fatalf("repeated worker close: err=%v closes=%d", err, directory.closed)
	}
}

func TestRunStepsIsolatesFailuresAndPanics(t *testing.T) {
	t.Parallel()
	var ran []string
	err := runSteps(t.Context(), []step{
		{"reminders", func(context.Context) error { ran = append(ran, "reminders"); return errors.New("bad reminder row") }},
		{"low stock", func(context.Context) error { ran = append(ran, "low stock"); panic("nil projection") }},
		{"jobs", func(context.Context) error { ran = append(ran, "jobs"); return nil }},
	})
	if !slices.Equal(ran, []string{"reminders", "low stock", "jobs"}) {
		t.Fatalf("a failing step starved later work: ran %v", ran)
	}
	var caught panicked
	if !errors.As(err, &caught) || caught.kind != "string" || !strings.HasPrefix(caught.path, "household/internal/worker.TestRunStepsIsolatesFailuresAndPanics.") {
		t.Fatalf("panic not recovered with its type and originating frame: %v", err)
	}
	if !strings.Contains(err.Error(), "reminders: bad reminder row") || !strings.Contains(err.Error(), "low stock: panic (string) at") {
		t.Fatalf("step failures lost their names: %v", err)
	}
	if strings.Contains(err.Error(), "nil projection") {
		t.Fatalf("panic value reached the error text: %v", err)
	}
	if err = runSteps(t.Context(), []step{{"jobs", func(context.Context) error { return nil }}}); err != nil {
		t.Fatalf("successful steps reported %v", err)
	}
}

func TestRunStepsStopsWhenCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	ran := false
	err := runSteps(ctx, []step{
		{"reminders", func(context.Context) error { cancel(); return errors.New("interrupted") }},
		{"jobs", func(context.Context) error { ran = true; return nil }},
	})
	if ran || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "reminders: interrupted") {
		t.Fatalf("shutdown continued background work or dropped a failure: ran=%v err=%v", ran, err)
	}
}
