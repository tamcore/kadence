// Package reindex re-embeds RAG chunks after the embedding model changes.
package reindex

import (
	"context"
	"log/slog"
	"time"
)

// backoff is the pause after a batch error before retrying. A var (not const)
// so tests can shorten it.
var backoff = 5 * time.Second

// Store is the chunk persistence the re-index worker needs.
type Store interface {
	AdoptUntagged(ctx context.Context) (int64, error)
	ReindexStatus(ctx context.Context) (stale, total int64, err error)
	ReembedBatch(ctx context.Context, embed func(context.Context, []string) ([][]float32, error), batch int) (int, error)
}

// Run adopts untagged chunks as current, then re-embeds stale chunks batch by
// batch until none remain (or ctx is cancelled). It never panics the caller;
// batch errors are logged and retried after a backoff. Safe to call on every
// startup — it is a no-op when nothing is stale.
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
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := s.ReembedBatch(ctx, embed, 0)
		if err != nil {
			log.Error("reindex: batch failed, backing off", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		if n == 0 {
			log.Info("reindex: complete")
			return
		}
		log.Info("reindex: batch done", "reembedded", n)
	}
}
