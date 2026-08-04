package serve

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/bg"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/push"
	"github.com/tamcore/kadence/internal/store"
)

// newPushHandler constructs the /api/push/* handler when web push is
// configured (cfg.PushEnabled), or returns nil otherwise. It is independent
// of cfg.ScheduledEnabled: subscribing/unsubscribing browsers is useful even
// before any scheduled digest exists to deliver, unlike startPushDispatcher
// which additionally requires ScheduledEnabled. Constructing a second
// *store.PushSubscriptionRepository here (alongside the one in
// startPushDispatcher) is fine — it's a stateless wrapper over the shared pool.
func newPushHandler(pool *pgxpool.Pool, cfg config.Config) *handlers.Push {
	if !cfg.PushEnabled() {
		return nil
	}
	return handlers.NewPush(cfg.PushVAPIDPublicKey, store.NewPushSubscriptionRepository(pool))
}

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
		bg.RunForever(ctx, slog.Default(), "push-dispatcher", dispatcher.Run)
	})
	return true
}
