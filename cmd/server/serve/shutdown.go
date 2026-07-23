package serve

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// shutdownServer calls srv.Shutdown with a fresh timeout context (deliberately
// not derived from rootCtx, which is already cancelled by the time shutdown
// starts): it stops accepting new connections, then waits up to timeout for
// active requests (including long-lived SSE chat streams) to finish on their
// own before giving up.
func shutdownServer(srv *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return srv.Shutdown(ctx)
}

// drainGoroutines waits up to timeout for wg to complete (i.e. for every
// background goroutine tracked by it — reindex worker, MCP health poller,
// session reaper — to observe its ctx cancellation and return). It reports
// whether the wait timed out, logging a warning in that case, so the caller
// can still proceed to close shared resources (e.g. the DB pool) rather than
// hang the process forever on a stuck goroutine.
func drainGoroutines(wg *sync.WaitGroup, timeout time.Duration, log *slog.Logger) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return false
	case <-time.After(timeout):
		log.Warn("background goroutines did not exit within drain timeout", "timeout", timeout)
		return true
	}
}
