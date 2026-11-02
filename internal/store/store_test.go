package store

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestStoreLockGate(t *testing.T) {
	t.Parallel()
	t.Run("waiting change does not block new readers", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			s := New(nil, nil)
			s.Gate.RLock() // a slow download or export still in flight
			acquired := make(chan error, 1)
			go func() { acquired <- s.lockGate(t.Context(), time.Minute) }()
			synctest.Wait()
			if !s.Gate.TryRLock() {
				t.Fatal("a waiting module change blocked a new request")
			}
			s.Gate.RUnlock()
			s.Gate.RUnlock()
			if err := <-acquired; err != nil {
				t.Fatalf("gate not acquired after readers finished: %v", err)
			}
			s.Gate.Unlock()
		})
	})
	t.Run("bounded wait reports busy", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			s := New(nil, nil)
			s.Gate.RLock()
			defer s.Gate.RUnlock()
			if err := s.lockGate(t.Context(), time.Second); !errors.Is(err, ErrBusy) {
				t.Fatalf("lockGate = %v, want ErrBusy", err)
			}
		})
	})
	t.Run("canceled request stops waiting", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			s := New(nil, nil)
			s.Gate.RLock()
			defer s.Gate.RUnlock()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := s.lockGate(ctx, time.Minute); !errors.Is(err, context.Canceled) {
				t.Fatalf("lockGate = %v, want context.Canceled", err)
			}
		})
	})
}
