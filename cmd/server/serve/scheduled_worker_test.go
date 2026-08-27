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
	ctx, cancel := context.WithCancel(t.Context())
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
	if startScheduledWorker(t.Context(), &sync.WaitGroup{}, true, nil) {
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

type scheduledRunnerFunc func(context.Context)

func (f scheduledRunnerFunc) Run(ctx context.Context) { f(ctx) }

func TestStartScheduledWorkerSurvivesPanickingWorker(t *testing.T) {
	// panicked is a size-1 buffer, not a one-shot latch: the send below only
	// blocks (falling through to default) while the buffer is still full,
	// i.e. before the test's `<-panicked` drains it. In practice that drain,
	// plus the immediate cancel() that follows it, race bg.RunForever's
	// restartBackoff (5s, not shortened here): ctx.Done() fires and
	// RunForever returns during that wait, so this worker is never invoked a
	// second time and the default branch below is not reached in this test.
	// It exists only so worker.Run stays safe if that timing ever changes.
	panicked := make(chan struct{}, 1)
	worker := scheduledRunnerFunc(func(ctx context.Context) {
		select {
		case panicked <- struct{}{}:
			panic("worker exploded")
		default:
			<-ctx.Done()
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	if !startScheduledWorker(ctx, &wg, true, worker) {
		t.Fatal("startScheduledWorker returned false for an enabled worker")
	}
	<-panicked
	// Reaching this line at all means the panic did not take the process down.
	cancel()
	wg.Wait()
}
