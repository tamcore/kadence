package serve

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/push"
	"github.com/tamcore/kadence/internal/store"
)

// startPushDispatcher wires and starts the push dispatcher goroutine when web
// push (cfg.PushEnabled) and scheduled digests (cfg.ScheduledEnabled) are both
// configured. It mirrors startScheduledWorker: it returns whether the
// dispatcher was started, and does nothing (returning false) otherwise.
func startPushDispatcher(ctx context.Context, wg *sync.WaitGroup, pool *pgxpool.Pool, cfg config.Config) bool {
	if !cfg.PushEnabled() || !cfg.ScheduledEnabled {
		return false
	}
	pushSubs := store.NewPushSubscriptionRepository(pool)
	pushSvc := push.NewService(
		pushSubs, cfg.PushVAPIDPublicKey, cfg.PushVAPIDPrivateKey, cfg.PushVAPIDSubject, slog.Default(),
	)
	dispatcher := push.NewDispatcher(pushSubs, pushSvc, push.DispatcherConfig{}, slog.Default())
	wg.Go(func() {
		dispatcher.Run(ctx)
	})
	return true
}
