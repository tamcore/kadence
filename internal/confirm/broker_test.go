package confirm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testUserID  = int64(42)
	otherUserID = int64(43)
	testTool    = "garmin__delete_workout"
	testPrompt  = "Delete workout 12?"
)

// awaitResult collects what a background Await returned, so a test can assert
// on it without racing the goroutine.
type awaitResult struct {
	ok  bool
	err error
}

// awaitInBackground starts Await and returns a channel carrying its single
// result. The channel is buffered so the goroutine never leaks on a failure.
func awaitInBackground(ctx context.Context, b *Broker, id string) <-chan awaitResult {
	out := make(chan awaitResult, 1)
	go func() {
		ok, err := b.Await(ctx, id)
		out <- awaitResult{ok, err}
	}()
	return out
}

// waitFor reads one result, failing the test rather than hanging forever.
func waitFor(t *testing.T, ch <-chan awaitResult) awaitResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("Await never returned")
		return awaitResult{}
	}
}

func newRequest(t *testing.T, b *Broker) Request {
	t.Helper()
	req, err := b.NewRequest(testUserID, testTool, testPrompt)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

func TestTheTTLStaysUnderTheTransportRequestCap(t *testing.T) {
	// mcp-go's streamable-http transport wraps every incoming server request
	// in context.WithTimeout(ctx, 30*time.Second) before calling the handler
	// (client/transport/streamable_http.go). An answer that arrives after that
	// reaches a context the transport has already abandoned, so the user's
	// decision is lost and the server sees REQUEST_INTERRUPTED instead.
	const transportRequestCap = 30 * time.Second
	if confirmTTL >= transportRequestCap {
		t.Fatalf("confirmTTL = %s, must be below the transport's %s cap", confirmTTL, transportRequestCap)
	}
}

func TestAnAllowedRequestUnblocksTheWaiter(t *testing.T) {
	b := NewBroker()
	req := newRequest(t, b)
	got := awaitInBackground(context.Background(), b, req.ID)

	if err := b.Submit(testUserID, req.ID, true); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	r := waitFor(t, got)
	if !r.ok || r.err != nil {
		t.Fatalf("Await = (%v, %v), want (true, nil)", r.ok, r.err)
	}
}

func TestADeclinedRequestReportsTheDecline(t *testing.T) {
	b := NewBroker()
	req := newRequest(t, b)
	got := awaitInBackground(context.Background(), b, req.ID)

	if err := b.Submit(testUserID, req.ID, false); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	r := waitFor(t, got)
	if r.ok || !errors.Is(r.err, ErrDeclined) {
		t.Fatalf("Await = (%v, %v), want (false, ErrDeclined)", r.ok, r.err)
	}
}

func TestAnAnswerIsSingleUse(t *testing.T) {
	b := NewBroker()
	req := newRequest(t, b)
	got := awaitInBackground(context.Background(), b, req.ID)

	if err := b.Submit(testUserID, req.ID, true); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	waitFor(t, got)
	if err := b.Submit(testUserID, req.ID, true); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("second Submit: %v, want ErrUnknownRequest", err)
	}
}

func TestAnotherUsersAnswerIsRefusedAndLeavesTheWaiterBlocked(t *testing.T) {
	b := NewBroker()
	req := newRequest(t, b)
	got := awaitInBackground(context.Background(), b, req.ID)

	if err := b.Submit(otherUserID, req.ID, true); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("foreign Submit: %v, want ErrUnknownRequest", err)
	}
	select {
	case r := <-got:
		t.Fatalf("a foreign answer released the waiter: (%v, %v)", r.ok, r.err)
	case <-time.After(50 * time.Millisecond):
	}

	// The rightful owner can still answer.
	if err := b.Submit(testUserID, req.ID, true); err != nil {
		t.Fatalf("owner Submit: %v", err)
	}
	if r := waitFor(t, got); !r.ok {
		t.Fatalf("Await = (%v, %v), want true", r.ok, r.err)
	}
}

func TestAnExpiredRequestTimesOutTheWaiterAndRefusesTheLateAnswer(t *testing.T) {
	clock := &testClock{at: time.Now()}
	b := NewBrokerWithClock(clock.Now)
	req := newRequest(t, b)

	clock.advance(confirmTTL + time.Second)
	got := awaitInBackground(context.Background(), b, req.ID)
	r := waitFor(t, got)
	if r.ok || !errors.Is(r.err, ErrTimedOut) {
		t.Fatalf("Await = (%v, %v), want (false, ErrTimedOut)", r.ok, r.err)
	}
	if err := b.Submit(testUserID, req.ID, true); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("late Submit: %v, want ErrUnknownRequest", err)
	}
}

func TestACancelledContextReleasesTheWaiter(t *testing.T) {
	b := NewBroker()
	req := newRequest(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	got := awaitInBackground(ctx, b, req.ID)

	cancel()
	r := waitFor(t, got)
	if r.ok || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("Await = (%v, %v), want (false, context.Canceled)", r.ok, r.err)
	}
}

func TestAnAnswerAfterACancelledWaitIsRefused(t *testing.T) {
	// Otherwise a Submit reports success for an answer nobody will ever read,
	// and the browser tells the user their decision was applied.
	b := NewBroker()
	req := newRequest(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	got := awaitInBackground(ctx, b, req.ID)

	cancel()
	waitFor(t, got)
	if err := b.Submit(testUserID, req.ID, true); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("Submit after cancel: %v, want ErrUnknownRequest", err)
	}
}

func TestSubmitNeverBlocksAgainstACancellation(t *testing.T) {
	// A Submit racing the waiter's cancellation must return either way. An
	// unbuffered handoff would deadlock here under -race.
	for range 200 {
		b := NewBroker()
		req := newRequest(t, b)
		ctx, cancel := context.WithCancel(context.Background())
		got := awaitInBackground(ctx, b, req.ID)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); _ = b.Submit(testUserID, req.ID, true) }()
		wg.Wait()
		waitFor(t, got)
	}
}

func TestAwaitingAnUnknownRequestReturnsAtOnce(t *testing.T) {
	b := NewBroker()
	got := awaitInBackground(context.Background(), b, "never-issued")
	r := waitFor(t, got)
	if r.ok || !errors.Is(r.err, ErrUnknownRequest) {
		t.Fatalf("Await = (%v, %v), want (false, ErrUnknownRequest)", r.ok, r.err)
	}
}

func TestTwoRequestsForOneUserAreIndependent(t *testing.T) {
	b := NewBroker()
	first := newRequest(t, b)
	second := newRequest(t, b)
	if first.ID == second.ID {
		t.Fatal("two requests shared an id")
	}
	gotFirst := awaitInBackground(context.Background(), b, first.ID)
	gotSecond := awaitInBackground(context.Background(), b, second.ID)

	if err := b.Submit(testUserID, first.ID, true); err != nil {
		t.Fatalf("Submit(first): %v", err)
	}
	if err := b.Submit(testUserID, second.ID, false); err != nil {
		t.Fatalf("Submit(second): %v", err)
	}
	if r := waitFor(t, gotFirst); !r.ok {
		t.Fatalf("first = (%v, %v), want true", r.ok, r.err)
	}
	if r := waitFor(t, gotSecond); !errors.Is(r.err, ErrDeclined) {
		t.Fatalf("second = (%v, %v), want ErrDeclined", r.ok, r.err)
	}
}

func TestPurgeUserReleasesThatUsersWaitersOnly(t *testing.T) {
	// A logout must not leave the turn hanging until the TTL elapses.
	b := NewBroker()
	mine := newRequest(t, b)
	theirs, err := b.NewRequest(otherUserID, testTool, testPrompt)
	if err != nil {
		t.Fatalf("NewRequest(other): %v", err)
	}
	gotMine := awaitInBackground(context.Background(), b, mine.ID)
	gotTheirs := awaitInBackground(context.Background(), b, theirs.ID)

	b.PurgeUser(testUserID)

	if r := waitFor(t, gotMine); r.ok || !errors.Is(r.err, ErrDeclined) {
		t.Fatalf("purged waiter = (%v, %v), want (false, ErrDeclined)", r.ok, r.err)
	}
	select {
	case r := <-gotTheirs:
		t.Fatalf("another user's waiter was released: (%v, %v)", r.ok, r.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestARequestCarriesTheToolAndThePrompt(t *testing.T) {
	b := NewBroker()
	req := newRequest(t, b)
	if req.Tool != testTool || req.Prompt != testPrompt || req.UserID != testUserID {
		t.Fatalf("request = %+v, want the submitted tool, prompt and user", req)
	}
	if req.ID == "" {
		t.Fatal("request has no id")
	}
}

// testClock is a hand-advanced clock, so expiry cases need no sleeping.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}
