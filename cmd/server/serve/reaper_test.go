package serve

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
)

const reaperTestTimeout = 2 * time.Second

// fakeReaperRepo is a test double for sessionReaper.
type fakeReaperRepo struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeReaperRepo) DeleteExpired(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

func (f *fakeReaperRepo) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// waitForCalls polls until repo has been called at least n times or the
// timeout elapses.
func waitForCalls(t *testing.T, repo *fakeReaperRepo, n int) {
	t.Helper()
	deadline := time.Now().Add(reaperTestTimeout)
	for repo.callCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d calls, got %d", n, repo.callCount())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRunSessionReaperRunsImmediatelyThenTicks(t *testing.T) {
	repo := &fakeReaperRepo{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runSessionReaper(ctx, repo, 5*time.Millisecond, slog.Default())
		close(done)
	}()

	waitForCalls(t, repo, 2) // one at startup, one from the first tick

	cancel()
	select {
	case <-done:
	case <-time.After(reaperTestTimeout):
		t.Fatal("runSessionReaper did not exit after context cancellation")
	}
}

func TestRunSessionReaperLogsErrorAndKeepsRunning(t *testing.T) {
	repo := &fakeReaperRepo{err: errors.New("boom")}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runSessionReaper(ctx, repo, 5*time.Millisecond, slog.Default())
		close(done)
	}()

	waitForCalls(t, repo, 2)

	cancel()
	select {
	case <-done:
	case <-time.After(reaperTestTimeout):
		t.Fatal("runSessionReaper did not exit after context cancellation despite errors")
	}
}

type fakeMCPAuditReaper struct {
	mu      sync.Mutex
	cutoffs []time.Time
}

func (f *fakeMCPAuditReaper) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	return 1, nil
}

func (f *fakeMCPAuditReaper) snapshot() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cutoffs)
}

func TestRunMCPAuditReaperRunsImmediatelyWithTTLThenTicks(t *testing.T) {
	repo := &fakeMCPAuditReaper{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	go func() {
		runMCPAuditReaper(ctx, repo, 48*time.Hour, 5*time.Millisecond, slog.Default(), func() time.Time { return now })
		close(done)
	}()

	deadline := time.Now().Add(reaperTestTimeout)
	for len(repo.snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cutoffs := repo.snapshot()
	if len(cutoffs) < 2 || !cutoffs[0].Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("cutoffs = %v", cutoffs)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(reaperTestTimeout):
		t.Fatal("runMCPAuditReaper did not exit after context cancellation")
	}
}
