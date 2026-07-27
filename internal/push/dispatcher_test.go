package push

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tamcore/kadence/internal/model"
)

// fakeClaimer returns its items on the first call, then an empty slice on
// subsequent calls, mimicking a repository that atomically claims rows.
type fakeClaimer struct {
	items []model.PendingPushDelivery
	calls int
}

func (f *fakeClaimer) ClaimUndispatchedDeliveries(_ context.Context, _ int) ([]model.PendingPushDelivery, error) {
	f.calls++
	if f.calls > 1 {
		return nil, nil
	}
	return f.items, nil
}

type sendCall struct {
	userID  int64
	payload Payload
}

type fakeSender struct {
	calls []sendCall
	err   error
}

func (f *fakeSender) SendToUser(_ context.Context, userID int64, p Payload) error {
	f.calls = append(f.calls, sendCall{userID: userID, payload: p})
	return f.err
}

func TestDispatchOnceBuildsPayloadAndSends(t *testing.T) {
	mid := int64(42)
	claimer := &fakeClaimer{items: []model.PendingPushDelivery{{
		RunID: 1, UserID: 7, TaskTitle: "Morning digest",
		ConversationID: "conv-uuid", MessageID: &mid,
		Result: strings.Repeat("x", 500),
	}}}
	sender := &fakeSender{}
	d := NewDispatcher(claimer, sender, DispatcherConfig{BatchLimit: 10, SnippetLen: 120}, slog.Default())

	n, err := d.dispatchOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 dispatched, got %d", n)
	}
	got := sender.calls[0]
	if got.userID != 7 {
		t.Fatalf("userID = %d", got.userID)
	}
	if got.payload.Title != "Scheduled digest: Morning digest" {
		t.Fatalf("title = %q", got.payload.Title)
	}
	if got.payload.URL != "/chat/conv-uuid#msg=42" {
		t.Fatalf("url = %q", got.payload.URL)
	}
	if utf8.RuneCountInString(got.payload.Body) > 121 {
		t.Fatalf("body not truncated: %d", utf8.RuneCountInString(got.payload.Body))
	}
}

func TestDispatchOnceOmitsAnchorWhenNoMessageID(t *testing.T) {
	claimer := &fakeClaimer{items: []model.PendingPushDelivery{{RunID: 1, UserID: 7, TaskTitle: "T", ConversationID: "c", MessageID: nil, Result: "r"}}}
	sender := &fakeSender{}
	d := NewDispatcher(claimer, sender, DispatcherConfig{BatchLimit: 10, SnippetLen: 120}, slog.Default())
	if _, err := d.dispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.calls[0].payload.URL != "/chat/c" {
		t.Fatalf("url = %q", sender.calls[0].payload.URL)
	}
}

func TestDispatchOnceReturnsZeroWhenNoItemsClaimed(t *testing.T) {
	claimer := &fakeClaimer{}
	sender := &fakeSender{}
	d := NewDispatcher(claimer, sender, DispatcherConfig{}, slog.Default())
	n, err := d.dispatchOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 dispatched, got %d", n)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("expected no sends, got %d", len(sender.calls))
	}
}
