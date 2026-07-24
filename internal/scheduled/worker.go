package scheduled

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tamcore/kadence/internal/model"
)

const (
	defaultWorkerPollInterval        = time.Second
	defaultWorkerMaintenanceInterval = time.Hour
	defaultWorkerStaleAfter          = 5 * time.Minute
	workerMaintenanceBatchSize       = 100
	noChangeRetention                = 30 * 24 * time.Hour
	failureInterrupted               = "execution_interrupted"
)

// WorkerStore coordinates every replica through atomic PostgreSQL claims.
type WorkerStore interface {
	ClaimDue(context.Context, time.Time, int) ([]model.ClaimedScheduledTask, error)
	ListStaleRunning(context.Context, time.Time, int) ([]model.ClaimedScheduledTask, error)
	FinishFailure(context.Context, ExecutionFailure) error
	DeleteExpiredNoChange(context.Context, time.Time) (int64, error)
}

// OccurrenceExecutor executes one already-claimed at-most-once occurrence.
type OccurrenceExecutor interface {
	Execute(context.Context, Actor, model.ClaimedScheduledTask) error
}

// WorkerConfig bounds one replica's polling and execution work.
type WorkerConfig struct {
	Concurrency         int
	PollInterval        time.Duration
	MaintenanceInterval time.Duration
	StaleAfter          time.Duration
}

// WorkerDeps are process-owned worker dependencies.
type WorkerDeps struct {
	Store    WorkerStore
	Executor OccurrenceExecutor
	Config   WorkerConfig
	Now      func() time.Time
	Log      *slog.Logger
}

// Worker polls due tasks on every application replica. PostgreSQL claims, not
// in-process coordination, provide cross-replica exclusivity.
type Worker struct {
	store    WorkerStore
	executor OccurrenceExecutor
	cfg      WorkerConfig
	now      func() time.Time
	log      *slog.Logger
}

// NewWorker builds a worker without starting any goroutines.
func NewWorker(deps WorkerDeps) *Worker {
	cfg := deps.Config
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultWorkerPollInterval
	}
	if cfg.MaintenanceInterval <= 0 {
		cfg.MaintenanceInterval = defaultWorkerMaintenanceInterval
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = defaultWorkerStaleAfter
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &Worker{store: deps.Store, executor: deps.Executor, cfg: cfg, now: now, log: log}
}

// Run performs boot recovery and retention, then polls until ctx is canceled.
// It does not return until every execution started by this replica has observed
// cancellation and exited.
func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.store == nil || w.executor == nil {
		return
	}
	now := w.now()
	w.recoverStale(ctx, now)
	w.cleanup(ctx, now)

	pollTicker := time.NewTicker(w.cfg.PollInterval)
	defer pollTicker.Stop()
	maintenanceTicker := time.NewTicker(w.cfg.MaintenanceInterval)
	defer maintenanceTicker.Stop()

	slots := make(chan struct{}, w.cfg.Concurrency)
	var executions sync.WaitGroup
	poll := func() {
		available := cap(slots) - len(slots)
		if available <= 0 {
			return
		}
		claims, err := w.store.ClaimDue(ctx, w.now(), available)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Warn("scheduled claim failed", "err", err)
			}
			return
		}
		for _, claim := range claims {
			slots <- struct{}{}
			executions.Go(func() {
				defer func() { <-slots }()
				actor := Actor{ID: claim.Task.UserID, Username: claim.Username}
				if err := w.executor.Execute(ctx, actor, claim); err != nil && ctx.Err() == nil {
					w.log.Warn("scheduled execution finished with error",
						"task_id", claim.Task.ID, "run_id", claim.Run.ID, "err", err)
				}
			})
		}
	}

	if ctx.Err() == nil {
		poll()
	}
	for {
		select {
		case <-ctx.Done():
			executions.Wait()
			return
		case <-pollTicker.C:
			poll()
		case tick := <-maintenanceTicker.C:
			w.recoverStale(ctx, tick)
			w.cleanup(ctx, tick)
		}
	}
}

func (w *Worker) recoverStale(ctx context.Context, now time.Time) {
	for {
		claims, err := w.store.ListStaleRunning(ctx, now.Add(-w.cfg.StaleAfter), workerMaintenanceBatchSize)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Warn("scheduled stale-run recovery failed", "err", err)
			}
			return
		}
		transitioned := 0
		for _, claim := range claims {
			failure := executionFailure(claim, now, failureInterrupted)
			if err := w.store.FinishFailure(ctx, failure); err != nil {
				if ctx.Err() == nil {
					w.log.Warn("scheduled stale-run transition lost", "run_id", claim.Run.ID, "err", err)
				}
			} else {
				transitioned++
			}
		}
		if len(claims) < workerMaintenanceBatchSize || transitioned == 0 || ctx.Err() != nil {
			return
		}
	}
}

func (w *Worker) cleanup(ctx context.Context, now time.Time) {
	deleted, err := w.store.DeleteExpiredNoChange(ctx, now.Add(-noChangeRetention))
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("scheduled no-change retention failed", "err", err)
		}
		return
	}
	if deleted > 0 {
		w.log.Info("scheduled no-change retention completed", "deleted", deleted)
	}
}

func executionFailure(claimed model.ClaimedScheduledTask, now time.Time, code string) ExecutionFailure {
	failure := ExecutionFailure{
		RunID: claimed.Run.ID, UserID: claimed.Task.UserID, Code: code,
		IncrementFailures: code != failureMissingTool,
		TaskState:         model.ScheduledTaskStateActive,
	}
	switch {
	case code == failureMissingTool:
		failure.Pause = true
		failure.TaskState = model.ScheduledTaskStatePaused
	case claimed.Task.OneOffAt != nil:
		failure.TaskState = model.ScheduledTaskStateFailed
	case claimed.Task.ConsecutiveFailures+1 >= 3:
		failure.Pause = true
		failure.TaskState = model.ScheduledTaskStatePaused
	default:
		next, err := scheduleFor(claimed.Task).NextAfter(now)
		if err != nil {
			failure.TaskState = model.ScheduledTaskStateFailed
		} else {
			failure.NextRunAt = &next
		}
	}
	return failure
}
