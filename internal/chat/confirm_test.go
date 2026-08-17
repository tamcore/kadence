package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/confirm"
)

const (
	confirmTestUserID = int64(42)
	confirmTestTool   = "garmin__delete_workout"
	confirmTestPrompt = "Delete workout 12? This cannot be undone."
)

// waitUntil polls cond until it holds, failing the test rather than hanging.
func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

// recordingSink captures the events a turn emitted.
type recordingSink struct {
	mu     sync.Mutex
	events []ChatEvent
}

func (s *recordingSink) Send(e ChatEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *recordingSink) Flush() error { return nil }

// firstConfirmEvent returns the confirmation event seen so far, if any.
func (s *recordingSink) firstConfirmEvent() (ChatEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Type == EventConfirm {
			return e, true
		}
	}
	return ChatEvent{}, false
}

// countingBroker counts how many questions were ever registered, which is what
// proves an unattended turn asked nobody.
type countingBroker struct {
	mu       sync.Mutex
	requests int
	inner    *confirm.Broker
}

func newCountingBroker() *countingBroker {
	return &countingBroker{inner: confirm.NewBroker()}
}

func (b *countingBroker) NewRequest(userID int64, tool, prompt string) (confirm.Request, error) {
	b.mu.Lock()
	b.requests++
	b.mu.Unlock()
	return b.inner.NewRequest(userID, tool, prompt)
}

func (b *countingBroker) Await(ctx context.Context, id string) (bool, error) {
	return b.inner.Await(ctx, id)
}

func (b *countingBroker) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests
}

// askInBackground starts one confirmation and returns the request id the user
// would answer, plus a channel carrying the eventual outcome.
type confirmOutcome struct {
	allowed bool
	err     error
}

func askInBackground(t *testing.T, bridge *ConfirmBridge, ctx context.Context, sink *recordingSink) (string, <-chan confirmOutcome) {
	t.Helper()
	out := make(chan confirmOutcome, 1)
	go func() {
		allowed, err := bridge.Confirm(ctx, confirmTestUserID, confirmTestTool, confirmTestPrompt)
		out <- confirmOutcome{allowed, err}
	}()

	var id string
	waitUntil(t, func() bool {
		e, ok := sink.firstConfirmEvent()
		id = e.RequestID
		return ok
	}, "the confirmation event never reached the sink")
	return id, out
}

func TestAConfirmationReachesTheLiveTurnAndReturnsTheAnswer(t *testing.T) {
	broker := newCountingBroker()
	bridge := NewConfirmBridge(broker)
	sink := &recordingSink{}
	ctx := WithConfirmSink(context.Background(), sink)

	reqID, out := askInBackground(t, bridge, ctx, sink)
	if err := broker.inner.Submit(confirmTestUserID, reqID, true); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	got := <-out
	if !got.allowed || got.err != nil {
		t.Fatalf("Confirm = (%v, %v), want (true, nil)", got.allowed, got.err)
	}
}

func TestTheConfirmationEventNamesTheToolAndCarriesThePrompt(t *testing.T) {
	broker := newCountingBroker()
	bridge := NewConfirmBridge(broker)
	sink := &recordingSink{}
	ctx, cancel := context.WithCancel(WithConfirmSink(context.Background(), sink))

	_, out := askInBackground(t, bridge, ctx, sink)
	cancel()
	<-out

	got, _ := sink.firstConfirmEvent()
	if got.Tool != confirmTestTool {
		t.Fatalf("tool = %q, want %q", got.Tool, confirmTestTool)
	}
	if got.Message != confirmTestPrompt {
		t.Fatalf("message = %q, want the server's own prompt", got.Message)
	}
	if got.RequestID == "" {
		t.Fatal("the event carries no request id, so nothing can be answered")
	}
}

func TestADeclinedConfirmationReportsFalseWithoutAnError(t *testing.T) {
	broker := newCountingBroker()
	bridge := NewConfirmBridge(broker)
	sink := &recordingSink{}
	ctx := WithConfirmSink(context.Background(), sink)

	reqID, out := askInBackground(t, bridge, ctx, sink)
	if err := broker.inner.Submit(confirmTestUserID, reqID, false); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	got := <-out
	if got.allowed {
		t.Fatal("a declined confirmation reported true")
	}
	if got.err != nil {
		t.Fatalf("a decline is an answer, not a fault: %v", got.err)
	}
}

func TestAnUnattendedTurnIsRefusedAtOnceAndAsksNobody(t *testing.T) {
	// A scheduled run has no live stream. It must refuse immediately rather
	// than register a question that would either hang for the TTL or, worse,
	// surface in some other turn of the same user.
	broker := newCountingBroker()
	bridge := NewConfirmBridge(broker)

	allowed, err := bridge.Confirm(context.Background(), confirmTestUserID, confirmTestTool, confirmTestPrompt)

	if allowed {
		t.Fatal("an unattended turn was allowed to run a tool needing confirmation")
	}
	if !errors.Is(err, ErrNoLiveTurn) {
		t.Fatalf("err = %v, want ErrNoLiveTurn", err)
	}
	if n := broker.count(); n != 0 {
		t.Fatalf("an unattended turn registered %d questions, want 0", n)
	}
}

func TestTheRefusalNamesConfirmationSoTheModelCanRelayIt(t *testing.T) {
	_, err := NewConfirmBridge(newCountingBroker()).
		Confirm(context.Background(), confirmTestUserID, confirmTestTool, confirmTestPrompt)
	if err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("err = %v, want it to mention confirmation", err)
	}
}
