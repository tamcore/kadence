package reindex

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type fakeStore struct {
	adopted   bool
	stale     int64
	total     int64
	batches   int
	failFirst bool
	batchErrs []error
}

func (f *fakeStore) AdoptUntagged(context.Context) (int64, error) { f.adopted = true; return 0, nil }
func (f *fakeStore) ReindexStatus(context.Context) (int64, int64, error) {
	return f.stale, f.total, nil
}
func (f *fakeStore) ReembedBatch(_ context.Context, _ func(context.Context, []string) ([][]float32, error), _ int) (int, error) {
	if len(f.batchErrs) > 0 {
		next := f.batchErrs[0]
		f.batchErrs = f.batchErrs[1:]
		if next != nil {
			return 0, next
		}
		return 1, nil
	}
	if f.failFirst {
		f.failFirst = false
		return 0, errors.New("transient")
	}
	if f.batches >= 2 {
		return 0, nil
	}
	f.batches++
	return 5, nil
}

func TestRun_AdoptsThenDrainsWithBackoff(t *testing.T) {
	restoreInitial := initialBackoff
	initialBackoff = time.Millisecond
	t.Cleanup(func() { initialBackoff = restoreInitial })
	f := &fakeStore{stale: 10, total: 10, failFirst: true}
	embed := func(context.Context, []string) ([][]float32, error) { return nil, nil }
	Run(t.Context(), f, embed, slog.Default())
	if !f.adopted {
		t.Fatal("expected AdoptUntagged to be called")
	}
	if f.batches != 2 {
		t.Fatalf("batches=%d, want 2 (drained after a transient error)", f.batches)
	}
}

func TestRunBackoffGrowsExponentiallyAndCapsAtCeiling(t *testing.T) {
	restoreInitial, restoreMax := initialBackoff, maxBackoff
	initialBackoff, maxBackoff = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { initialBackoff, maxBackoff = restoreInitial, restoreMax })

	var delays []time.Duration
	restoreSleep := sleepFn
	sleepFn = func(ctx context.Context, d time.Duration) bool {
		delays = append(delays, d)
		return ctx.Err() == nil
	}
	t.Cleanup(func() { sleepFn = restoreSleep })

	s := &fakeStore{
		stale: 10, total: 10,
		batchErrs: []error{
			errors.New("e1"), errors.New("e2"), errors.New("e3"), errors.New("e4"),
		},
	}
	Run(context.Background(), s, func(context.Context, []string) ([][]float32, error) {
		return nil, nil
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	want := []time.Duration{
		time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond,
	}
	if len(delays) != len(want) {
		t.Fatalf("recorded %d delays (%v), want %d", len(delays), delays, len(want))
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delay[%d] = %s, want %s (full: %v)", i, delays[i], want[i], delays)
		}
	}
}

func TestRunBackoffResetsAfterASuccessfulBatch(t *testing.T) {
	restoreInitial, restoreMax := initialBackoff, maxBackoff
	initialBackoff, maxBackoff = time.Millisecond, 8*time.Millisecond
	t.Cleanup(func() { initialBackoff, maxBackoff = restoreInitial, restoreMax })

	var delays []time.Duration
	restoreSleep := sleepFn
	sleepFn = func(ctx context.Context, d time.Duration) bool {
		delays = append(delays, d)
		return ctx.Err() == nil
	}
	t.Cleanup(func() { sleepFn = restoreSleep })

	// err, err, success, err  ->  1ms, 2ms, (reset), 1ms
	s := &fakeStore{
		stale: 10, total: 10,
		batchErrs: []error{errors.New("e1"), errors.New("e2"), nil, errors.New("e3")},
	}
	Run(context.Background(), s, func(context.Context, []string) ([][]float32, error) {
		return nil, nil
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, time.Millisecond}
	if len(delays) < len(want) {
		t.Fatalf("recorded %v, want at least %v", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delay[%d] = %s, want %s (full: %v)", i, delays[i], want[i], delays)
		}
	}
}
