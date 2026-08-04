// Package reindex re-embeds RAG chunks after the embedding model changes.
package reindex

import (
	"context"
	"log/slog"
	"time"
)

// Backoff bounds for the retry loop after a batch error. Vars (not consts) so
// tests can shorten them. Error-path only — deliberately not configurable.
var (
	initialBackoff = 5 * time.Second
	maxBackoff     = 5 * time.Minute
)

// sleepFn waits d or until ctx is done, reporting whether the full wait
// elapsed. A var so tests can observe the delays without real sleeping.
var sleepFn = func(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// Store is the chunk persistence the re-index worker needs.
type Store interface {
	AdoptUntagged(ctx context.Context) (int64, error)
	ReindexStatus(ctx context.Context) (stale, total int64, err error)
	ReembedBatch(ctx context.Context, embed func(context.Context, []string) ([][]float32, error), batch int) (int, error)
}

// Run adopts untagged chunks as current, then re-embeds stale chunks batch by
// batch until none remain (or ctx is cancelled). Batch errors are logged and
// retried with exponential backoff. Panic containment is the caller's
// responsibility (bg.RunForever wraps this at the call site). Safe to call on
// every startup — it is a no-op when nothing is stale.
func Run(ctx context.Context, s Store, embed func(context.Context, []string) ([][]float32, error), log *slog.Logger) {
	if _, err := s.AdoptUntagged(ctx); err != nil {
		log.Error("reindex: adopt untagged failed", "err", err)
		return
	}
	stale, total, err := s.ReindexStatus(ctx)
	if err != nil {
		log.Error("reindex: status failed", "err", err)
		return
	}
	if stale == 0 {
		return
	}
	log.Info("reindex: starting", "stale", stale, "total", total)
	delay := initialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := s.ReembedBatch(ctx, embed, 0)
		if err != nil {
			log.Error("reindex: batch failed, backing off", "err", err, "delay", delay)
			if !sleepFn(ctx, delay) {
				return
			}
			if delay < maxBackoff {
				delay *= 2
				if delay > maxBackoff {
					delay = maxBackoff
				}
			}
			continue
		}
		delay = initialBackoff
		if n == 0 {
			log.Info("reindex: complete")
			return
		}
		log.Info("reindex: batch done", "reembedded", n)
	}
}
