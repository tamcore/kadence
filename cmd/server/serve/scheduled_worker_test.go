package serve

import (
	"context"
	"sync"
	"testing"
	"time"
)

type scheduledWorkerRunnerStub struct {
	started chan struct{}
	stopped chan struct{}
}

func (w *scheduledWorkerRunnerStub) Run(ctx context.Context) {
	close(w.started)
	<-ctx.Done()
	close(w.stopped)
}

func TestStartScheduledWorkerOnlyWhenEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	disabled := &scheduledWorkerRunnerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	if startScheduledWorker(ctx, &wg, false, disabled) {
		t.Fatal("disabled worker reported started")
	}
	select {
	case <-disabled.started:
		t.Fatal("disabled worker goroutine started")
	default:
	}

	enabled := &scheduledWorkerRunnerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	if !startScheduledWorker(ctx, &wg, true, enabled) {
		t.Fatal("enabled worker did not report started")
	}
	select {
	case <-enabled.started:
	case <-time.After(time.Second):
		t.Fatal("enabled worker did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enabled worker was not tracked by background waitgroup")
	}
	select {
	case <-enabled.stopped:
	default:
		t.Fatal("waitgroup finished before worker stopped")
	}
}

func TestStartScheduledWorkerRejectsNilRunner(t *testing.T) {
	if startScheduledWorker(context.Background(), &sync.WaitGroup{}, true, nil) {
		t.Fatal("nil worker reported started")
	}
}

func TestScheduledStaleAfterCoversGatherAndSynthesis(t *testing.T) {
	gather := 5 * time.Minute
	synthesis := 2 * time.Minute
	got := scheduledStaleAfter(gather, synthesis)
	if got <= gather+synthesis {
		t.Fatalf("stale cutoff = %s, want beyond full %s execution budget", got, gather+synthesis)
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if got := scheduledStaleAfter(maxDuration, time.Second); got != maxDuration {
		t.Fatalf("overflow stale cutoff = %s, want %s", got, maxDuration)
	}
}
