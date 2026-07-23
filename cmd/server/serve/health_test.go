package serve

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHealthzHandlerReturns200 verifies the liveness endpoint's contract: a
// plain 200 with no body requirements, so kubelet's default httpGet probe
// (which only checks the status code) always passes.
func TestHealthzHandlerReturns200(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	healthzHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthzHandler status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestHealthListenerStaysUpDuringMainServerShutdown reproduces the scenario
// the dedicated health listener exists to fix: while the main server is
// mid-Shutdown (draining a slow in-flight request), the health listener must
// keep answering /healthz with 200 — that's what keeps kubelet's liveness
// probe green for the whole graceful-drain window. Only after both the main
// server's Shutdown call AND the caller-driven health shutdown complete
// should the health listener stop responding.
func TestHealthListenerStaysUpDuringMainServerShutdown(t *testing.T) {
	handlerReleased := make(chan struct{})
	mainSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-handlerReleased
			w.WriteHeader(http.StatusOK)
		}),
	}
	mainLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen (main): %v", err)
	}
	go func() { _ = mainSrv.Serve(mainLn) }()

	healthSrv := newHealthServer("")
	healthLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen (health): %v", err)
	}
	go func() { _ = healthSrv.Serve(healthLn) }()

	client := http.Client{Timeout: shutdownTestTimeout}

	// Kick off a slow in-flight request against the main server so its
	// Shutdown call has something to wait on.
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		resp, rErr := client.Get("http://" + mainLn.Addr().String())
		if rErr == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(10 * time.Millisecond)

	// Start the main server's graceful shutdown in the background; it will
	// block until the handler above is released.
	mainShutdownDone := make(chan error, 1)
	go func() {
		mainShutdownDone <- shutdownServer(mainSrv, shutdownTestTimeout)
	}()

	// While the main server is (still) mid-drain, the health listener must
	// answer 200.
	time.Sleep(20 * time.Millisecond)
	hzResp, hzErr := client.Get("http://" + healthLn.Addr().String() + "/healthz")
	if hzErr != nil {
		t.Fatalf("GET /healthz during main drain: %v", hzErr)
	}
	_ = hzResp.Body.Close()
	if hzResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz during main drain = %d, want %d", hzResp.StatusCode, http.StatusOK)
	}

	close(handlerReleased)
	<-reqDone
	if err := <-mainShutdownDone; err != nil {
		t.Fatalf("shutdownServer(mainSrv) = %v, want nil", err)
	}

	// Health listener must still be up immediately after the main server has
	// finished draining — it is only shut down once the caller explicitly
	// does so (mirroring Run's ordering: main shutdown, then goroutine
	// drain, then health shutdown).
	hzResp2, hzErr2 := client.Get("http://" + healthLn.Addr().String() + "/healthz")
	if hzErr2 != nil {
		t.Fatalf("GET /healthz after main shutdown, before health shutdown: %v", hzErr2)
	}
	_ = hzResp2.Body.Close()
	if hzResp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz after main shutdown = %d, want %d", hzResp2.StatusCode, http.StatusOK)
	}

	if err := shutdownServer(healthSrv, shutdownTestTimeout); err != nil {
		t.Fatalf("shutdownServer(healthSrv) = %v, want nil", err)
	}

	if _, err := client.Get("http://" + healthLn.Addr().String() + "/healthz"); err == nil {
		t.Fatal("GET /healthz after health shutdown = nil error, want connection refused")
	}
}
