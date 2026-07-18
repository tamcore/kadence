package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
)

type fakeStreamer struct{ gotText string }

func (f *fakeStreamer) Stream(_ context.Context, _, _ int64, text string, sink chat.EventSink) error {
	f.gotText = text
	_ = sink.Send(chat.ChatEvent{Type: chat.EventMeta, ConversationID: 5})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventToken, Delta: "hi"})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone})
	return sink.Flush()
}

type fakeConvLister struct{ list []model.Conversation }

func (f fakeConvLister) ListByUser(context.Context, int64) ([]model.Conversation, error) {
	return f.list, nil
}
func (f fakeConvLister) GetByID(_ context.Context, id, userID int64) (model.Conversation, error) {
	return model.Conversation{ID: id, UserID: userID}, nil
}
func (f fakeConvLister) Delete(context.Context, int64, int64) error { return nil }

type fakeMsgLister struct{ msgs []model.Message }

func (f fakeMsgLister) ListByConversation(context.Context, int64) ([]model.Message, error) {
	return f.msgs, nil
}

func withUser(r *http.Request, id int64) *http.Request {
	return r.WithContext(auth.ContextWithUser(r.Context(), &model.User{ID: id, Username: "u", Role: model.RoleUser}))
}

func TestChatSendStreamsSSE(t *testing.T) {
	fs := &fakeStreamer{}
	h := handlers.NewChat(fs, fakeConvLister{}, fakeMsgLister{})

	req := withUser(httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(`{"message":"hello there"}`)), 7)
	rec := httptest.NewRecorder()
	h.Send(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"meta"`) || !strings.Contains(body, `"type":"done"`) {
		t.Fatalf("sse body missing events: %s", body)
	}
	if fs.gotText != "hello there" {
		t.Fatalf("streamer got %q", fs.gotText)
	}
}

func TestChatSendRequiresUser(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, fakeConvLister{}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.Send(rec, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestListConversations(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, fakeConvLister{list: []model.Conversation{{ID: 1, Title: "a"}}}, fakeMsgLister{})
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/conversations", nil), 7)
	rec := httptest.NewRecorder()
	h.ListConversations(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"a"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
