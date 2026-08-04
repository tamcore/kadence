package bg

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuardConvertsPanicToPanicError(t *testing.T) {
	err := Guard(func() error { panic("boom") })
	if err == nil {
		t.Fatal("expected an error from a panicking fn, got nil")
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *PanicError: %T", err)
	}
	if pe.Value != "boom" {
		t.Fatalf("panic value = %v, want boom", pe.Value)
	}
	if !strings.Contains(string(pe.Stack), "TestGuardConvertsPanicToPanicError") {
		t.Fatalf("stack does not name the panicking test:\n%s", pe.Stack)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Error() = %q, want it to mention boom", err.Error())
	}
}

func TestGuardPassesNormalErrorThrough(t *testing.T) {
	sentinel := errors.New("plain failure")
	err := Guard(func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the sentinel unchanged", err)
	}
	var pe *PanicError
	if errors.As(err, &pe) {
		t.Fatal("a normal error must not be wrapped as *PanicError")
	}
}

func TestGuardReturnsNilOnSuccess(t *testing.T) {
	if err := Guard(func() error { return nil }); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestRunForeverRestartsAfterPanicAndLogsStack(t *testing.T) {
	restoreBackoff := restartBackoff
	restartBackoff = time.Millisecond
	t.Cleanup(func() { restartBackoff = restoreBackoff })

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	var calls atomic.Int32
	started := make(chan struct{}, 3)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunForever(ctx, log, "testsub", func(context.Context) {
			started <- struct{}{}
			if calls.Add(1) < 3 {
				panic("loop exploded")
			}
			<-ctx.Done()
		})
		close(done)
	}()

	for range 3 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("fn was not restarted; calls = %d", calls.Load())
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunForever did not return after ctx cancel")
	}

	out := logs.String()
	if !strings.Contains(out, "testsub") {
		t.Fatalf("log does not name the subsystem:\n%s", out)
	}
	if !strings.Contains(out, "loop exploded") {
		t.Fatalf("log does not carry the panic value:\n%s", out)
	}
	if !strings.Contains(out, "level=ERROR") {
		t.Fatalf("panic must be logged at ERROR:\n%s", out)
	}
}

func TestRunForeverReturnsOnCancelWithoutRestarting(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunForever(ctx, slog.Default(), "testsub", func(c context.Context) {
			calls.Add(1)
			<-c.Done()
		})
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunForever did not return after ctx cancel")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want exactly 1 (a clean return must not restart)", got)
	}
}

func TestRunForeverDoesNotRestartAfterCancelDuringBackoff(t *testing.T) {
	restoreBackoff := restartBackoff
	restartBackoff = time.Hour // long enough that only ctx can end the wait
	t.Cleanup(func() { restartBackoff = restoreBackoff })

	var calls atomic.Int32
	panicked := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunForever(ctx, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), "testsub",
			func(context.Context) {
				calls.Add(1)
				panicked <- struct{}{}
				panic("once")
			})
		close(done)
	}()
	<-panicked
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunForever did not abandon its backoff wait on ctx cancel")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}
}

func TestRunForeverToleratesNilLogger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RunForever(ctx, nil, "testsub", func(context.Context) {})
}
