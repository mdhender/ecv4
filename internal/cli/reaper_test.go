package cli

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakePurger records the cutoffs it was called with. It is safe for concurrent
// use because the reaper's sweep runs on its own goroutine.
type fakePurger struct {
	mu      sync.Mutex
	cutoffs []int64
	fired   chan struct{} // signalled after each sweep
	err     error
}

func (f *fakePurger) PurgeExpiredRefreshTokens(_ context.Context, cutoff int64) (int64, error) {
	f.mu.Lock()
	f.cutoffs = append(f.cutoffs, cutoff)
	f.mu.Unlock()
	select {
	case f.fired <- struct{}{}:
	default:
	}
	return 0, f.err
}

func (f *fakePurger) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

// TestReaperSweepsThenStopsOnCancel verifies the reaper runs an immediate sweep
// at startup, keeps sweeping on the interval, and returns promptly once its
// context is cancelled.
func TestReaperSweepsThenStopsOnCancel(t *testing.T) {
	p := &fakePurger{fired: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())

	now := func() time.Time { return time.Unix(1000, 0) }

	done := make(chan struct{})
	go func() {
		runRefreshTokenReaper(ctx, p, 5*time.Millisecond, now, quietLogger())
		close(done)
	}()

	// Immediate startup sweep.
	select {
	case <-p.fired:
	case <-time.After(time.Second):
		t.Fatal("reaper did not perform a startup sweep")
	}
	// At least one more ticked sweep.
	select {
	case <-p.fired:
	case <-time.After(time.Second):
		t.Fatal("reaper did not perform a scheduled sweep")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reaper did not stop after context cancellation")
	}

	// Every sweep used the clock's cutoff (unix seconds), proving now() is wired.
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.cutoffs {
		if c != 1000 {
			t.Fatalf("cutoff = %d, want 1000", c)
		}
	}
}

// TestReaperDisabledWithNonPositiveInterval verifies a non-positive interval
// turns the reaper off entirely: it returns without ever sweeping.
func TestReaperDisabledWithNonPositiveInterval(t *testing.T) {
	p := &fakePurger{fired: make(chan struct{}, 1)}
	runRefreshTokenReaper(context.Background(), p, 0, time.Now, quietLogger())
	if p.calls() != 0 {
		t.Fatalf("disabled reaper made %d calls, want 0", p.calls())
	}
}
