package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
)

// slowStreamer blocks longer than the (shortened) keepalive interval before
// finishing, so the keepalive ticker fires at least once.
type slowStreamer struct{ delay time.Duration }

func (s slowStreamer) Stream(_ context.Context, _ int64, _ chat.UserContext, _ string, _ string, sink chat.EventSink) error {
	_ = sink.Send(chat.ChatEvent{Type: chat.EventMeta, ConversationID: "conv-uuid-1"})
	_ = sink.Flush()
	time.Sleep(s.delay)
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone})
	return sink.Flush()
}

// TestSendEmitsKeepaliveDuringLongTurn verifies a keepalive comment is written
// while the stream is still running, and (via the goroutine join in Send) that
// no write happens after the handler returns.
func TestSendEmitsKeepaliveDuringLongTurn(t *testing.T) {
	orig := sseKeepaliveInterval
	sseKeepaliveInterval = 5 * time.Millisecond
	defer func() { sseKeepaliveInterval = orig }()

	h := NewChat(slowStreamer{delay: 40 * time.Millisecond}, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"hi"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 7, Username: "u", Role: model.RoleUser}))
	rec := httptest.NewRecorder()

	h.Send(rec, req)

	if !strings.Contains(rec.Body.String(), ": keepalive") {
		t.Fatalf("expected a keepalive comment in the SSE stream; got:\n%s", rec.Body.String())
	}
}
