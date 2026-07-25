package serve

import (
	"context"
	"log/slog"
	"time"
)

// sessionReapInterval is how often the session reaper sweeps expired
// sessions. Fixed at 1h; no env knob (YAGNI) per the session-reaper design.
const sessionReapInterval = time.Hour
const mcpAuditReapInterval = time.Hour

// sessionReaper is the session persistence the reaper needs.
type sessionReaper interface {
	DeleteExpired(ctx context.Context) (int64, error)
}

type mcpAuditReaper interface {
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// runSessionReaper deletes expired sessions once immediately (so a restart
// doesn't wait a full interval before the first sweep), then again every
// interval, until ctx is cancelled. A repo error is logged at warn and does
// not stop the loop; the next tick tries again.
func runSessionReaper(ctx context.Context, repo sessionReaper, interval time.Duration, log *slog.Logger) {
	reapExpiredSessions(ctx, repo, log)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reapExpiredSessions(ctx, repo, log)
		}
	}
}

// reapExpiredSessions runs one DeleteExpired sweep, logging the outcome.
func reapExpiredSessions(ctx context.Context, repo sessionReaper, log *slog.Logger) {
	n, err := repo.DeleteExpired(ctx)
	if err != nil {
		log.Warn("session reaper: delete expired sessions failed", "err", err)
		return
	}
	if n > 0 {
		log.Info("session reaper: deleted expired sessions", "count", n)
	}
}

func runMCPAuditReaper(
	ctx context.Context, repo mcpAuditReaper, ttl, interval time.Duration,
	log *slog.Logger, now func() time.Time,
) {
	reapExpiredMCPAudit(ctx, repo, ttl, log, now)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reapExpiredMCPAudit(ctx, repo, ttl, log, now)
		}
	}
}

func reapExpiredMCPAudit(
	ctx context.Context, repo mcpAuditReaper, ttl time.Duration,
	log *slog.Logger, now func() time.Time,
) {
	n, err := repo.DeleteBefore(ctx, now().Add(-ttl))
	if err != nil {
		log.Warn("MCP audit reaper: delete expired calls failed", "err", err)
		return
	}
	if n > 0 {
		log.Info("MCP audit reaper: deleted expired calls", "count", n)
	}
}
