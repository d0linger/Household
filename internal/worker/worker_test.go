package worker

import (
	"context"
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
