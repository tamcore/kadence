package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

type fakeProvider struct{ reply string }

func (f fakeProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	for _, part := range []string{f.reply[:2], f.reply[2:]} {
		if err := onToken(part); err != nil {
			return "", err
		}
	}
	return f.reply, nil
}

type fakeConvs struct {
	created *model.Conversation
	byID    map[int64]model.Conversation
}

func (f *fakeConvs) Create(_ context.Context, userID int64, title string) (model.Conversation, error) {
	c := model.Conversation{ID: 42, UserID: userID, Title: title}
	f.created = &c
	return c, nil
}
func (f *fakeConvs) GetByID(_ context.Context, id, userID int64) (model.Conversation, error) {
	if c, ok := f.byID[id]; ok && c.UserID == userID {
		return c, nil
	}
	return model.Conversation{}, errFakeNotFound
}

var errFakeNotFound = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "not found" }

type fakeMsgs struct{ added []model.Message }

func (f *fakeMsgs) Add(_ context.Context, convID int64, role, content string) (model.Message, error) {
	m := model.Message{ID: int64(len(f.added) + 1), ConversationID: convID, Role: role, Content: content}
	f.added = append(f.added, m)
	return m, nil
}
func (f *fakeMsgs) ListByConversation(_ context.Context, _ int64) ([]model.Message, error) {
	return f.added, nil
}

type capturingSink struct{ events []chat.ChatEvent }

func (s *capturingSink) Send(e chat.ChatEvent) error { s.events = append(s.events, e); return nil }
func (s *capturingSink) Flush() error                { return nil }

const testReply = "Hello!"

func TestStreamNewConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: "m", MaxTokens: 64, Temperature: 0.2, SystemPrompt: "You are a coach."},
		convs, msgs)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 7, 0, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if sink.events[0].Type != chat.EventMeta || sink.events[0].ConversationID != 42 {
		t.Fatalf("first event = %+v, want meta with conv id", sink.events[0])
	}
	if sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("last event = %+v, want done", sink.events[len(sink.events)-1])
	}
	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if streamed.String() != testReply {
		t.Fatalf("streamed = %q", streamed.String())
	}
	if len(msgs.added) != 2 || msgs.added[0].Role != model.MsgRoleUser || msgs.added[1].Role != model.MsgRoleAssistant || msgs.added[1].Content != testReply {
		t.Fatalf("persisted messages wrong: %+v", msgs.added)
	}
	if convs.created == nil {
		t.Fatal("expected a conversation to be created")
	}
}
