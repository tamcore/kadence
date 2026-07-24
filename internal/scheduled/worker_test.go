package scheduled

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
)

type workerStoreStub struct {
	mu            sync.Mutex
	claims        []model.ClaimedScheduledTask
	stale         []model.ClaimedScheduledTask
	claimCalls    int
	staleCalls    int
	cleanupCalls  int
	claimErr      error
	staleErr      error
	cleanupErr    error
	failures      []ExecutionFailure
	finishError   error
	finishErrors  map[int64]error
	cleanupBefore time.Time
	staleBefore   time.Time
	claimedLimits []int
}

func (s *workerStoreStub) ClaimDue(_ context.Context, _ time.Time, limit int) ([]model.ClaimedScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls++
	s.claimedLimits = append(s.claimedLimits, limit)
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if limit > len(s.claims) {
		limit = len(s.claims)
	}
	claims := append([]model.ClaimedScheduledTask(nil), s.claims[:limit]...)
	s.claims = s.claims[limit:]
	return claims, nil
}

func (s *workerStoreStub) ListStaleRunning(_ context.Context, before time.Time, limit int) ([]model.ClaimedScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.staleCalls++
	s.staleBefore = before
	if s.staleErr != nil {
		return nil, s.staleErr
	}
	if limit > len(s.stale) {
		limit = len(s.stale)
	}
	claims := append([]model.ClaimedScheduledTask(nil), s.stale[:limit]...)
	s.stale = s.stale[limit:]
	return claims, nil
}

func (s *workerStoreStub) FinishFailure(_ context.Context, failure ExecutionFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure)
	if err, ok := s.finishErrors[failure.RunID]; ok {
		return err
	}
	return s.finishError
}

func (s *workerStoreStub) DeleteExpiredNoChange(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupCalls++
	s.cleanupBefore = before
	return 1, s.cleanupErr
}

type workerExecutorStub struct {
	started chan model.ClaimedScheduledTask
	release chan struct{}
	active  atomic.Int32
	max     atomic.Int32
}

type workerExecutorFunc func(context.Context, Actor, model.ClaimedScheduledTask) error

func (f workerExecutorFunc) Execute(ctx context.Context, actor Actor, claim model.ClaimedScheduledTask) error {
	return f(ctx, actor, claim)
}

func (e *workerExecutorStub) Execute(ctx context.Context, _ Actor, claim model.ClaimedScheduledTask) error {
	active := e.active.Add(1)
	defer e.active.Add(-1)
	for {
		maximum := e.max.Load()
		if active <= maximum || e.max.CompareAndSwap(maximum, active) {
			break
		}
	}
	if e.started != nil {
		e.started <- claim
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.release:
		return nil
	}
}

func workerClaim(id int64, scheduledFor time.Time) model.ClaimedScheduledTask {
	started := scheduledFor
	return model.ClaimedScheduledTask{
		Task: model.ScheduledTask{
			ID: executorTestTaskID, UserID: id, State: model.ScheduledTaskStateActive,
			Kind: model.ScheduledTaskKindReminder, Timezone: "UTC",
			RRULE: executorDailyRRULE, DTStart: new(scheduledFor.Add(-24 * time.Hour)),
		},
		Run: model.ScheduledTaskRun{
			ID: id, TaskID: executorTestTaskID, State: model.ScheduledTaskRunStateRunning,
			ScheduledFor: scheduledFor, StartedAt: &started,
		},
		Username: "owner",
	}
}

func TestWorkerPollsDueTasksWithBoundedConcurrency(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := &workerStoreStub{claims: []model.ClaimedScheduledTask{
		workerClaim(1, now), workerClaim(2, now), workerClaim(3, now),
	}}
	executor := &workerExecutorStub{
		started: make(chan model.ClaimedScheduledTask, 3),
		release: make(chan struct{}, 3),
	}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: executor,
		Config: WorkerConfig{Concurrency: 2, PollInterval: time.Millisecond},
		Now:    func() time.Time { return now },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	<-executor.started
	<-executor.started
	select {
	case third := <-executor.started:
		t.Fatalf("third task started before capacity was released: %+v", third)
	case <-time.After(20 * time.Millisecond):
	}
	executor.release <- struct{}{}
	<-executor.started
	if maximum := executor.max.Load(); maximum != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", maximum)
	}
	executor.release <- struct{}{}
	executor.release <- struct{}{}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, limit := range store.claimedLimits {
		if limit > 2 {
			t.Fatalf("ClaimDue limit = %d, want at most configured concurrency", limit)
		}
	}
}

func TestWorkerBootRecoveryAndRetention(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	recurring := workerClaim(7, now.Add(-2*time.Hour))
	recurring.Task.ConsecutiveFailures = 1
	store := &workerStoreStub{stale: []model.ClaimedScheduledTask{recurring}}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: &workerExecutorStub{release: make(chan struct{})},
		Config: WorkerConfig{
			Concurrency: 1, PollInterval: time.Hour,
			StaleAfter: time.Hour, MaintenanceInterval: time.Hour,
		},
		Now: func() time.Time { return now },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.Run(ctx)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.staleCalls != 1 || !store.staleBefore.Equal(now.Add(-time.Hour)) {
		t.Fatalf("stale recovery calls=%d before=%s", store.staleCalls, store.staleBefore)
	}
	if len(store.failures) != 1 {
		t.Fatalf("recovered failures = %+v, want one", store.failures)
	}
	failure := store.failures[0]
	if failure.Code != failureInterrupted || !failure.IncrementFailures ||
		failure.TaskState != model.ScheduledTaskStateActive || failure.NextRunAt == nil ||
		!failure.NextRunAt.After(now) {
		t.Fatalf("recovery failure = %+v", failure)
	}
	if store.cleanupCalls != 1 || !store.cleanupBefore.Equal(now.Add(-noChangeRetention)) {
		t.Fatalf("cleanup calls=%d before=%s", store.cleanupCalls, store.cleanupBefore)
	}
}

func TestWorkerRecoveryAppliesFailurePolicyAndToleratesRaces(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	oneOff := workerClaim(8, now.Add(-2*time.Hour))
	oneOff.Task.OneOffAt = new(now.Add(-2 * time.Hour))
	oneOff.Task.RRULE = ""
	oneOff.Task.DTStart = nil
	thirdFailure := workerClaim(9, now.Add(-2*time.Hour))
	thirdFailure.Task.ConsecutiveFailures = 2
	store := &workerStoreStub{
		stale:       []model.ClaimedScheduledTask{oneOff, thirdFailure},
		finishError: errors.New("lost recovery race"),
	}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: &workerExecutorStub{release: make(chan struct{})},
		Config: WorkerConfig{Concurrency: 2},
		Now:    func() time.Time { return now },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	worker.recoverStale(context.Background(), now)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failures) != 2 {
		t.Fatalf("recovered failures = %d, want 2", len(store.failures))
	}
	if store.failures[0].TaskState != model.ScheduledTaskStateFailed || store.failures[0].NextRunAt != nil {
		t.Fatalf("one-off recovery = %+v", store.failures[0])
	}
	if !store.failures[1].Pause || store.failures[1].TaskState != model.ScheduledTaskStatePaused {
		t.Fatalf("third failure recovery = %+v", store.failures[1])
	}
}

func TestWorkerBootRecoveryDrainsEveryStaleBatch(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	stale := make([]model.ClaimedScheduledTask, 205)
	for i := range stale {
		stale[i] = workerClaim(int64(i+1), now.Add(-2*time.Hour))
	}
	store := &workerStoreStub{stale: stale}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: &workerExecutorStub{release: make(chan struct{})},
		Config: WorkerConfig{Concurrency: 1, StaleAfter: time.Hour},
		Now:    func() time.Time { return now },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	worker.recoverStale(context.Background(), now)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failures) != len(stale) {
		t.Fatalf("recovered failures = %d, want %d", len(store.failures), len(stale))
	}
	if store.staleCalls != 3 {
		t.Fatalf("stale batch calls = %d, want 3", store.staleCalls)
	}
}

func TestWorkerRecoveryContinuesAfterPartialCASProgress(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	stale := make([]model.ClaimedScheduledTask, 205)
	lost := make(map[int64]error)
	for i := range stale {
		stale[i] = workerClaim(int64(i+1), now.Add(-2*time.Hour))
		if i%2 == 0 && i < workerMaintenanceBatchSize {
			lost[stale[i].Run.ID] = errors.New("lost recovery CAS")
		}
	}
	store := &workerStoreStub{stale: stale, finishErrors: lost}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: &workerExecutorStub{release: make(chan struct{})},
		Config: WorkerConfig{Concurrency: 1, StaleAfter: time.Hour},
		Now:    func() time.Time { return now },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	worker.recoverStale(context.Background(), now)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.staleCalls != 3 {
		t.Fatalf("partial-progress batch calls = %d, want 3", store.staleCalls)
	}

	allFailed := make([]model.ClaimedScheduledTask, workerMaintenanceBatchSize)
	for i := range allFailed {
		allFailed[i] = workerClaim(int64(i+1), now.Add(-2*time.Hour))
	}
	noProgress := &workerStoreStub{
		stale: allFailed, finishError: errors.New("database unavailable"),
	}
	worker.store = noProgress
	worker.recoverStale(context.Background(), now)
	noProgress.mu.Lock()
	defer noProgress.mu.Unlock()
	if noProgress.staleCalls != 1 {
		t.Fatalf("zero-progress batch calls = %d, want 1", noProgress.staleCalls)
	}
}

func TestWorkerShutdownCancelsAndWaitsForExecutions(t *testing.T) {
	now := time.Now()
	store := &workerStoreStub{claims: []model.ClaimedScheduledTask{workerClaim(1, now)}}
	executor := &workerExecutorStub{
		started: make(chan model.ClaimedScheduledTask, 1),
		release: make(chan struct{}),
	}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: executor,
		Config: WorkerConfig{Concurrency: 1, PollInterval: time.Hour},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	<-executor.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run returned without canceling and waiting for execution")
	}
	if active := executor.active.Load(); active != 0 {
		t.Fatalf("active executions after shutdown = %d, want 0", active)
	}
	store.mu.Lock()
	claimCalls := store.claimCalls
	store.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.claimCalls != claimCalls {
		t.Fatalf("worker continued polling after shutdown: %d -> %d", claimCalls, store.claimCalls)
	}
}

func TestNewWorkerDefaultsAndMissingDependencies(t *testing.T) {
	worker := NewWorker(WorkerDeps{})
	if worker.cfg.Concurrency != 1 || worker.cfg.PollInterval != defaultWorkerPollInterval ||
		worker.cfg.MaintenanceInterval != defaultWorkerMaintenanceInterval ||
		worker.cfg.StaleAfter != defaultWorkerStaleAfter || worker.now == nil || worker.log == nil {
		t.Fatalf("worker defaults = %+v", worker.cfg)
	}
	(*Worker)(nil).Run(context.Background())
	worker.Run(context.Background())
	worker.store = &workerStoreStub{}
	worker.Run(context.Background())
}

func TestWorkerLogsPollingAndExecutionErrors(t *testing.T) {
	now := time.Now()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	store := &workerStoreStub{
		claims: []model.ClaimedScheduledTask{workerClaim(1, now), workerClaim(2, now)},
	}
	executed := make(chan struct{}, 1)
	worker := NewWorker(WorkerDeps{
		Store: store,
		Executor: workerExecutorFunc(func(context.Context, Actor, model.ClaimedScheduledTask) error {
			executed <- struct{}{}
			return errors.New("executor failed")
		}),
		Config: WorkerConfig{Concurrency: 1, PollInterval: time.Hour},
		Log:    log,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	<-executed
	cancel()
	<-done
	if !bytes.Contains(logs.Bytes(), []byte("scheduled execution finished with error")) {
		t.Fatalf("missing execution error log: %s", logs.String())
	}

	logs.Reset()
	store = &workerStoreStub{claimErr: errors.New("claim failed")}
	worker = NewWorker(WorkerDeps{
		Store: store, Executor: workerExecutorFunc(func(context.Context, Actor, model.ClaimedScheduledTask) error { return nil }),
		Config: WorkerConfig{PollInterval: time.Hour}, Log: log,
	})
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	for {
		store.mu.Lock()
		calls := store.claimCalls
		store.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if !bytes.Contains(logs.Bytes(), []byte("scheduled claim failed")) {
		t.Fatalf("missing claim error log: %s", logs.String())
	}

}

func TestWorkerMaintenanceTicksAndLogsFailures(t *testing.T) {
	now := time.Now()
	var logs bytes.Buffer
	store := &workerStoreStub{}
	worker := NewWorker(WorkerDeps{
		Store: store, Executor: workerExecutorFunc(func(context.Context, Actor, model.ClaimedScheduledTask) error { return nil }),
		Config: WorkerConfig{
			PollInterval: time.Hour, MaintenanceInterval: time.Millisecond,
		},
		Now: func() time.Time { return now },
		Log: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		calls := store.cleanupCalls
		store.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("maintenance ticker did not run")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	store.staleErr = errors.New("stale failed")
	worker.recoverStale(context.Background(), now)
	store.staleErr = nil
	store.cleanupErr = errors.New("cleanup failed")
	worker.cleanup(context.Background(), now)
	if !bytes.Contains(logs.Bytes(), []byte("scheduled stale-run recovery failed")) ||
		!bytes.Contains(logs.Bytes(), []byte("scheduled no-change retention failed")) {
		t.Fatalf("missing maintenance error logs: %s", logs.String())
	}
}
