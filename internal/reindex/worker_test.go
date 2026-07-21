package reindex

import (
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
}

func (f *fakeStore) AdoptUntagged(context.Context) (int64, error) { f.adopted = true; return 0, nil }
func (f *fakeStore) ReindexStatus(context.Context) (int64, int64, error) {
	return f.stale, f.total, nil
}
func (f *fakeStore) ReembedBatch(_ context.Context, _ func(context.Context, []string) ([][]float32, error), _ int) (int, error) {
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
	backoff = time.Millisecond
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
