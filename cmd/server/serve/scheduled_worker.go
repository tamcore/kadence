package serve

import (
	"context"
	"sync"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/scheduled"
)

const scheduledFinalizationMargin = 30 * time.Second

type scheduledWorkerRunner interface {
	Run(context.Context)
}

func startScheduledWorker(ctx context.Context, wg *sync.WaitGroup, enabled bool, worker scheduledWorkerRunner) bool {
	if !enabled || worker == nil {
		return false
	}
	wg.Go(func() {
		worker.Run(ctx)
	})
	return true
}

type scheduledToolsAdapter struct {
	catalog *chat.UnattendedCatalog
}

func (a scheduledToolsAdapter) SnapshotFor(
	ctx context.Context,
	username string,
) (scheduled.ExecutionToolSnapshot, error) {
	return a.catalog.SnapshotFor(ctx, username)
}

func scheduledStaleAfter(gather, synthesis time.Duration) time.Duration {
	const maxDuration = time.Duration(1<<63 - 1)
	if gather > maxDuration-scheduledFinalizationMargin ||
		synthesis > maxDuration-scheduledFinalizationMargin-gather {
		return maxDuration
	}
	return gather + synthesis + scheduledFinalizationMargin
}
