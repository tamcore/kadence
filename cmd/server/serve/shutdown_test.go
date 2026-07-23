package serve

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const shutdownTestTimeout = 2 * time.Second

// TestShutdownServerDrainsInFlightRequest verifies that shutdownServer waits
// for a slow in-flight handler to finish (well within its timeout) instead of
// cutting the connection, and returns nil once the handler completes.
func TestShutdownServerDrainsInFlightRequest(t *testing.T) {
	handlerDone := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(handlerDone)
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()

	client := http.Client{Timeout: shutdownTestTimeout}
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		resp, rErr := client.Get("http://" + ln.Addr().String())
		if rErr == nil {
			_ = resp.Body.Close()
		}
	}()

	// Give the request a moment to reach the handler before shutdown starts,
	// so shutdownServer must actually wait rather than racing an empty
	// connection table.
	time.Sleep(10 * time.Millisecond)

	if err := shutdownServer(srv, shutdownTestTimeout); err != nil {
		t.Fatalf("shutdownServer() = %v, want nil", err)
	}

	select {
	case <-handlerDone:
	default:
		t.Fatal("shutdownServer returned before the in-flight handler finished")
	}
	<-reqDone
}

// TestShutdownServerTimeoutReturnsError verifies that shutdownServer surfaces
// the context-deadline error when a handler outlives the shutdown timeout.
func TestShutdownServerTimeoutReturnsError(t *testing.T) {
	release := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	defer close(release)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()

	go func() {
		client := http.Client{Timeout: shutdownTestTimeout}
		resp, rErr := client.Get("http://" + ln.Addr().String())
		if rErr == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(10 * time.Millisecond)

	if err := shutdownServer(srv, 20*time.Millisecond); err == nil {
		t.Fatal("shutdownServer() = nil, want deadline-exceeded error")
	}
}

// fakeDrainer is a testable stand-in for the background goroutines that run
// on rootCtx (reindex, health poller, session reaper): it exits once ctx is
// cancelled and marks the WaitGroup done.
func fakeDrainer(ctx context.Context, wg *sync.WaitGroup, delay time.Duration) {
	defer wg.Done()
	<-ctx.Done()
	if delay > 0 {
		time.Sleep(delay)
	}
}

func TestDrainGoroutinesReturnsPromptlyWhenGoroutinesExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go fakeDrainer(ctx, &wg, 0)
	go fakeDrainer(ctx, &wg, 0)

	cancel()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	timedOut := drainGoroutines(&wg, shutdownTestTimeout, log)

	if timedOut {
		t.Fatal("drainGoroutines() = true, want false (goroutines exited in time)")
	}
	if strings.Contains(buf.String(), "did not exit") {
		t.Fatalf("unexpected timeout log: %s", buf.String())
	}
}

func TestDrainGoroutinesLogsAndReturnsOnTimeout(t *testing.T) {
	ctx := context.Background() // never cancelled: goroutine never exits
	var wg sync.WaitGroup
	wg.Add(1)
	go fakeDrainer(ctx, &wg, 0)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	timedOut := drainGoroutines(&wg, 20*time.Millisecond, log)

	if !timedOut {
		t.Fatal("drainGoroutines() = false, want true (goroutine never exits)")
	}
	if !strings.Contains(buf.String(), "did not exit") {
		t.Fatalf("expected timeout log, got: %s", buf.String())
	}
}
