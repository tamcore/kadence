package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

type fakeProvider struct {
	reply string
	err   error
}

func (f fakeProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	if f.err != nil {
		return "", f.err
	}
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

const (
	testReply     = "Hello!"
	testSystemMsg = "You are a coach."
	testModel     = "m"
	testMaxTokens = 64
	testTemp      = 0.2
	testUserID    = 7
	testConvID    = 5
	testConvTitle = "test"
)

func TestStreamNewConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		convs, msgs, nil, nil)

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

func TestStreamExistingConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		convs, msgs, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if sink.events[0].Type != chat.EventMeta || sink.events[0].ConversationID != testConvID {
		t.Fatalf("first event = %+v, want meta with conv id %d", sink.events[0], testConvID)
	}
	if convs.created != nil {
		t.Fatal("should not create new conversation when id provided")
	}
}

func TestStreamConversationNotFound(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		convs, msgs, nil, nil)

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, 99, "hi coach", sink)
	if err == nil || err.Error() != "conversation not found" {
		t.Fatalf("expected 'conversation not found' error, got: %v", err)
	}
	if len(sink.events) == 0 || sink.events[0].Type != chat.EventError {
		t.Fatalf("expected error event, got: %v", sink.events)
	}
}

func TestStreamProviderError(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{err: &providerErr{}},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		convs, msgs, nil, nil)

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, testConvID, "hi coach", sink)
	if err == nil || err.Error() != "the assistant could not complete the response" {
		t.Fatalf("expected provider error, got: %v", err)
	}
	if len(sink.events) == 0 || sink.events[len(sink.events)-1].Type != chat.EventError {
		t.Fatalf("expected error event in sink, got: %v", sink.events)
	}
}

type providerErr struct{}

func (*providerErr) Error() string { return "provider failed" }

const testTimeout = 5 * time.Second

type deadlineAssertingProvider struct {
	t     *testing.T
	reply string
}

func (p deadlineAssertingProvider) StreamChat(ctx context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		p.t.Fatal("expected ctx to have a deadline when ServiceConfig.Timeout is set")
	}
	if err := onToken(p.reply); err != nil {
		return "", err
	}
	return p.reply, nil
}

func TestStreamAppliesTimeout(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(deadlineAssertingProvider{t: t, reply: testReply},
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp,
			SystemPrompt: testSystemMsg, Timeout: testTimeout,
		},
		convs, msgs, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

// recordingProvider records whether StreamChat was called; returns a canned reply.
const (
	testGuardrailClassifierModel = "c"
	testGuardrailDomain          = "Coach"
	testGuardrailTopics          = "training"
	testGuardrailRefusal         = "nope, coaching only"
)

type recordingProvider struct{ called bool }

func (p *recordingProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.called = true
	_ = onToken("hello")
	return "hello", nil
}

func TestStreamGuardrailRefusesOffTopic(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	mainP := &recordingProvider{}
	guard := chat.NewGuardrail(&verdictProvider{verdict: "OFF_TOPIC"}, chat.GuardrailConfig{
		Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain, AllowedTopics: testGuardrailTopics,
		RefusalMessage: testGuardrailRefusal, HistoryWindow: 6,
	})
	svc := chat.NewService(mainP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, convs, msgs, guard, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 1, 0, "what's the stock market doing?", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if mainP.called {
		t.Fatal("main provider should NOT be called on refusal")
	}
	last := msgs.added[len(msgs.added)-1]
	if last.Role != model.MsgRoleAssistant || last.Content != testGuardrailRefusal {
		t.Fatalf("refusal not persisted: %+v", last)
	}
	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if streamed.String() != testGuardrailRefusal {
		t.Fatalf("streamed = %q", streamed.String())
	}
}

func TestStreamGuardrailFailsOpen(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	mainP := &recordingProvider{}
	guard := chat.NewGuardrail(&verdictProvider{err: errors.New("classifier down")}, chat.GuardrailConfig{
		Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain, AllowedTopics: testGuardrailTopics,
		RefusalMessage: "nope", HistoryWindow: 6,
	})
	svc := chat.NewService(mainP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, convs, msgs, guard, nil)

	if err := svc.Stream(context.Background(), 1, 0, "how many rest days?", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !mainP.called {
		t.Fatal("guardrail error must fail open → main provider called")
	}
}

// capturingProvider records the messages it was asked to stream.
type capturingProvider struct {
	reply       string
	gotMessages []provider.Message
}

func (p *capturingProvider) StreamChat(_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.gotMessages = req.Messages
	_ = onToken(p.reply)
	return p.reply, nil
}

func TestStreamInjectsRAGContextAndStores(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	fc := &fakeChunks{search: []model.Chunk{{Content: "you prefer morning runs"}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, convs, msgs, nil, rag)

	if err := svc.Stream(context.Background(), 7, 0, "plan my week", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var hasNote bool
	for _, m := range captP.gotMessages {
		if m.Role == "system" && strings.Contains(m.Content, "you prefer morning runs") {
			hasNote = true
		}
	}
	if !hasNote {
		t.Fatalf("RAG context not injected: %+v", captP.gotMessages)
	}
	if len(fc.inserted) != 2 {
		t.Fatalf("expected 2 chunks stored (user+assistant), got %d", len(fc.inserted))
	}
}
