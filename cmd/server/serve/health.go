package serve

import (
	"errors"
	"log/slog"
	"net/http"
)

// healthzHandler answers the dedicated liveness listener: a plain 200 with no
// auth, no body parsing, and no access logging (this endpoint is polled every
// few seconds by kubelet for the life of the pod, so logging it would be pure
// noise).
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// newHealthServer builds the dedicated liveness-only *http.Server: it serves
// nothing but GET /healthz on its own listener/port (KADENCE_HEALTH_ADDR),
// separate from the main server's /api/healthz. Run starts this before the
// main server and shuts it down LAST — after the main server's Shutdown and
// the background-goroutine drain both complete — so kubelet's liveness probe
// (which targets this listener) stays green for the entire graceful-drain
// window. Readiness intentionally stays on the main listener's /api/healthz:
// readiness failing during drain is desired, since that's what removes the
// pod from Service endpoints.
func newHealthServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
}

// startHealthServer starts srv listening in the background and reports fatal
// listen errors (e.g. address already in use) on errCh, mirroring how the
// main server reports ListenAndServe errors in Run.
func startHealthServer(srv *http.Server, errCh chan<- error) {
	go func() {
		slog.Info("health listener starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
}
