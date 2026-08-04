// Package bg contains the panic-containment policy for long-lived background
// goroutines. chi's Recoverer covers HTTP handlers only: a panic in a goroutine
// started with sync.WaitGroup.Go terminates the process, so every background
// subsystem entry point routes through this package instead.
package bg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// restartBackoff is the pause before RunForever restarts a panicked loop. A var
// rather than a const so tests can shorten it, matching the existing convention
// in internal/reindex. Error-path only — deliberately not configurable.
var restartBackoff = 5 * time.Second

// PanicError carries a value recovered from a panic together with the stack
// captured at recovery time.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// Guard runs fn, converting a panic into a *PanicError. An error returned
// normally by fn is passed through unchanged.
func Guard(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return fn()
}

// RunForever runs fn until ctx is done. If fn panics, RunForever logs at ERROR
// with the subsystem name and the recovered stack, waits out restartBackoff, then
// runs fn again — a single poison input must not silently stop a whole subsystem.
// If fn returns normally it is treated as finished (it watches ctx itself) and
// RunForever returns without restarting it.
func RunForever(ctx context.Context, log *slog.Logger, name string, fn func(context.Context)) {
	if log == nil {
		log = slog.Default()
	}
	for ctx.Err() == nil {
		err := Guard(func() error {
			fn(ctx)
			return nil
		})
		if err == nil {
			return
		}
		var pe *PanicError
		if errors.As(err, &pe) {
			log.Error("background subsystem panicked, restarting",
				"subsystem", name,
				"panic", fmt.Sprint(pe.Value),
				"stack", string(pe.Stack))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(restartBackoff):
		}
	}
}
