package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/chat/skill"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/secret"
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

func (f fakeProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := f.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

// scriptedProvider returns a pre-scripted StreamResult per call, streaming
// each result's content through onToken, so tests can exercise multi-call
// flows like truncation-continuation.
type scriptedProvider struct {
	results []provider.StreamResult
	calls   int
}

func (p *scriptedProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	r, err := p.StreamChatWithTools(ctx, req, onToken)
	return r.Content, err
}

func (p *scriptedProvider) StreamChatWithTools(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	if p.calls >= len(p.results) {
		return provider.StreamResult{FinishReason: "stop"}, nil
	}
	r := p.results[p.calls]
	p.calls++
	if r.Content != "" {
		if err := onToken(r.Content); err != nil {
			return provider.StreamResult{}, err
		}
	}
	return r, nil
}

type fakeConvs struct {
	created *model.Conversation
	byID    map[string]model.Conversation
}

func (f *fakeConvs) Create(_ context.Context, userID int64, title string) (model.Conversation, error) {
	c := model.Conversation{ID: testNewConvID, UserID: userID, Title: title}
	f.created = &c
	return c, nil
}
func (f *fakeConvs) GetByID(_ context.Context, id string, userID int64) (model.Conversation, error) {
	if c, ok := f.byID[id]; ok && c.UserID == userID {
		return c, nil
	}
	return model.Conversation{}, errFakeNotFound
}

var errFakeNotFound = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "not found" }

type fakeMsgs struct{ added []model.Message }

func (f *fakeMsgs) Add(_ context.Context, convID string, role, content string) (model.Message, error) {
	m := model.Message{ID: int64(len(f.added) + 1), ConversationID: convID, Role: role, Content: content}
	f.added = append(f.added, m)
	return m, nil
}
func (f *fakeMsgs) AddWithToolCalls(_ context.Context, convID string, role, content string, toolCalls []model.MessageToolCall) (model.Message, error) {
	m := model.Message{ID: int64(len(f.added) + 1), ConversationID: convID, Role: role, Content: content, ToolCalls: toolCalls}
	f.added = append(f.added, m)
	return m, nil
}
func (f *fakeMsgs) ListByConversation(_ context.Context, _ string) ([]model.Message, error) {
	return f.added, nil
}

type capturingSink struct{ events []chat.ChatEvent }

func (s *capturingSink) Send(e chat.ChatEvent) error { s.events = append(s.events, e); return nil }
func (s *capturingSink) Flush() error                { return nil }

// syncCapturingSink is a mutex-guarded capturingSink for tests where a
// goroutine polls sink.events concurrently with Stream still running (e.g.
// waiting for a credentials_request event to submit values for). Plain
// capturingSink is not safe for that concurrent read/write pattern.
type syncCapturingSink struct {
	mu     sync.Mutex
	events []chat.ChatEvent
}

func (s *syncCapturingSink) Send(e chat.ChatEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}
func (s *syncCapturingSink) Flush() error { return nil }

// snapshot returns a copy of the events recorded so far.
func (s *syncCapturingSink) snapshot() []chat.ChatEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]chat.ChatEvent, len(s.events))
	copy(out, s.events)
	return out
}

const (
	testReply     = "Hello!"
	testSystemMsg = "You are a coach."
	testModel     = "m"
	testMaxTokens = 64
	testTemp      = 0.2
	testUserID    = 7
	testUsername  = "alice"
	testConvID    = "conv-uuid-1"
	testNewConvID = "conv-uuid-new"
	testConvTitle = "test"
)

func TestStreamNewConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if sink.events[0].Type != chat.EventMeta || sink.events[0].ConversationID != testNewConvID {
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
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if sink.events[0].Type != chat.EventMeta || sink.events[0].ConversationID != testConvID {
		t.Fatalf("first event = %+v, want meta with conv id %s", sink.events[0], testConvID)
	}
	if convs.created != nil {
		t.Fatal("should not create new conversation when id provided")
	}
}

func TestStreamConversationNotFound(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, testUsername, "", "missing-uuid", "hi coach", sink)
	if err == nil || err.Error() != "conversation not found" {
		t.Fatalf("expected 'conversation not found' error, got: %v", err)
	}
	if len(sink.events) == 0 || sink.events[0].Type != chat.EventError {
		t.Fatalf("expected error event, got: %v", sink.events)
	}
}

func TestStreamProviderError(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{err: &providerErr{}},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, testUsername, "", testConvID, "hi coach", sink)
	if err == nil || err.Error() != "the assistant could not complete the response" {
		t.Fatalf("expected provider error, got: %v", err)
	}
	if len(sink.events) == 0 || sink.events[len(sink.events)-1].Type != chat.EventError {
		t.Fatalf("expected error event in sink, got: %v", sink.events)
	}
}

func TestStreamContinuesTruncatedAnswer(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	// First completion stops on "length" (hit the token cap); the second
	// finishes normally. The service should stitch them into one answer.
	p := &scriptedProvider{results: []provider.StreamResult{
		{Content: "part one ", FinishReason: provider.FinishLength},
		{Content: "part two.", FinishReason: "stop"},
	}}
	svc := chat.NewService(p,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial + one continuation)", p.calls)
	}

	const want = "part one part two."
	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if streamed.String() != want {
		t.Fatalf("streamed = %q, want %q", streamed.String(), want)
	}
	assistant := msgs.added[len(msgs.added)-1]
	if assistant.Role != model.MsgRoleAssistant || assistant.Content != want {
		t.Fatalf("persisted assistant = %+v, want content %q", assistant, want)
	}
}

func TestStreamStopsContinuingAtCap(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	// Every completion reports "length": the service must not loop forever.
	results := make([]provider.StreamResult, 10)
	for i := range results {
		results[i] = provider.StreamResult{Content: "x", FinishReason: provider.FinishLength}
	}
	p := &scriptedProvider{results: results}
	svc := chat.NewService(p,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	if err := svc.Stream(context.Background(), testUserID, testUsername, "", testConvID, "hi coach", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// initial call + maxContinuations (3) = 4, then it gives up.
	if p.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (initial + 3 continuations)", p.calls)
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

func (p deadlineAssertingProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestStreamAppliesTimeout(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(deadlineAssertingProvider{t: t, reply: testReply},
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp,
			SystemPrompt: testSystemMsg, Timeout: testTimeout,
		},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", testConvID, "hi coach", sink); err != nil {
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

func (p *recordingProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestStreamGuardrailRefusesOffTopic(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mainP := &recordingProvider{}
	guard := chat.NewGuardrail(&verdictProvider{verdict: "OFF_TOPIC"}, chat.GuardrailConfig{
		Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain, AllowedTopics: testGuardrailTopics,
		RefusalMessage: testGuardrailRefusal, HistoryWindow: 6,
	})
	svc := chat.NewService(mainP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, chat.Deps{Convs: convs, Msgs: msgs, Guardrail: guard})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 1, testUsername, "", "", "what's the stock market doing?", sink); err != nil {
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
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mainP := &recordingProvider{}
	guard := chat.NewGuardrail(&verdictProvider{err: errors.New("classifier down")}, chat.GuardrailConfig{
		Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain, AllowedTopics: testGuardrailTopics,
		RefusalMessage: "nope", HistoryWindow: 6,
	})
	svc := chat.NewService(mainP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, chat.Deps{Convs: convs, Msgs: msgs, Guardrail: guard})

	if err := svc.Stream(context.Background(), 1, testUsername, "", "", "how many rest days?", &capturingSink{}); err != nil {
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

func (p *capturingProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestStreamSystemPromptIncludesTodaysDate(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	fixed := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	svc := chat.NewService(captP,
		chat.ServiceConfig{Model: "m", MaxTokens: 32, Now: func() time.Time { return fixed }},
		chat.Deps{Convs: convs, Msgs: msgs})

	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "what's my next workout", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var systemContent string
	for _, m := range captP.gotMessages {
		if m.Role == model.MsgRoleSystem {
			systemContent = m.Content
		}
	}
	for _, want := range []string{"2026-07-19", fixed.Weekday().String()} {
		if !strings.Contains(systemContent, want) {
			t.Fatalf("system prompt missing %q; got: %s", want, systemContent)
		}
	}
}

func TestDefaultSystemPromptIsSlimAndPointsToSkills(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs})
	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "hi", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sys string
	for _, m := range captP.gotMessages {
		if m.Role == model.MsgRoleSystem {
			sys = m.Content
		}
	}
	if !strings.Contains(sys, "load_skill") {
		t.Fatalf("system prompt should point to load_skill; got: %s", sys)
	}
	if strings.Contains(sys, "sets, reps, and rest") {
		t.Fatal("workout guidance should have moved out of the base prompt")
	}
}

func TestMemorySkillInjectedWithRAGNotes(t *testing.T) {
	reg, _ := skill.Load()
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	fc := &fakeChunks{search: []model.Chunk{{Content: "you prefer morning runs"}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs, RAG: rag, Skills: reg})
	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "plan my week", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var joined strings.Builder
	for _, m := range captP.gotMessages {
		if m.Role == model.MsgRoleSystem {
			joined.WriteString("\n" + m.Content)
		}
	}
	if !strings.Contains(joined.String(), "authoritative history") {
		t.Fatalf("memory skill should be injected when RAG notes are present; system msgs: %s", joined.String())
	}
}

func TestMemorySkillNotInjectedWithoutNotes(t *testing.T) {
	reg, _ := skill.Load()
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	fc := &fakeChunks{search: nil} // no notes
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs, RAG: rag, Skills: reg})
	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "hi", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, m := range captP.gotMessages {
		if m.Role == model.MsgRoleSystem && strings.Contains(m.Content, "authoritative history") {
			t.Fatal("memory skill must not be injected when there are no RAG notes")
		}
	}
}

func TestStreamInjectsRAGContextAndStores(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	fc := &fakeChunks{search: []model.Chunk{{Content: "you prefer morning runs"}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, chat.Deps{Convs: convs, Msgs: msgs, RAG: rag})

	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "plan my week", &capturingSink{}); err != nil {
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

// TestStreamPersistsToolCallsOnAssistantMessage verifies the turn's tool calls
// (name + arguments) are recorded on the persisted assistant message, closing
// the post-hoc audit gap.
func TestStreamPersistsToolCallsOnAssistantMessage(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	mcp := &fakeMCPTools{enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}}, callResult: testToolReply}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "what's the weather", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	last := msgs.added[len(msgs.added)-1]
	if last.Role != model.MsgRoleAssistant {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("persisted tool calls = %d, want 1 (%+v)", len(last.ToolCalls), last.ToolCalls)
	}
	if last.ToolCalls[0].Name != testToolName || last.ToolCalls[0].Arguments != testToolArgs {
		t.Fatalf("persisted tool call = %+v, want {%s %s}", last.ToolCalls[0], testToolName, testToolArgs)
	}
}

// toolThenContentProvider returns a tool call on the first StreamChatWithTools
// call and plain content on the second.
type toolThenContentProvider struct {
	toolName    string
	toolArgs    string
	finalReply  string
	calls       int
	gotMessages [][]provider.Message
}

func (p *toolThenContentProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", errors.New("StreamChat should not be called when tools are in play")
}

func (p *toolThenContentProvider) StreamChatWithTools(_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	p.gotMessages = append(p.gotMessages, req.Messages)
	p.calls++
	if p.calls == 1 {
		return provider.StreamResult{
			ToolCalls: []provider.ToolCall{{ID: "call_1", Name: p.toolName, Arguments: p.toolArgs}},
		}, nil
	}
	if err := onToken(p.finalReply); err != nil {
		return provider.StreamResult{}, err
	}
	return provider.StreamResult{Content: p.finalReply}, nil
}

// alwaysToolProvider always returns a tool call, to exercise max-iterations.
type alwaysToolProvider struct {
	toolName string
	calls    int
}

func (p *alwaysToolProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", errors.New("StreamChat should not be called when tools are in play")
}

func (p *alwaysToolProvider) StreamChatWithTools(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (provider.StreamResult, error) {
	p.calls++
	return provider.StreamResult{
		ToolCalls: []provider.ToolCall{{ID: "call", Name: p.toolName, Arguments: "{}"}},
	}, nil
}

// fakeMCPTools is a canned MCPTools implementation for tests.
type fakeMCPTools struct {
	enabled     bool
	tools       []provider.ToolDefinition
	callResult  string
	callErr     error
	gotUsername string
	gotToolName string
	gotArgsJSON string
	callInvoked bool
}

func (f *fakeMCPTools) Enabled() bool { return f.enabled }

func (f *fakeMCPTools) ToolsFor(_ context.Context, _ string) ([]provider.ToolDefinition, error) {
	return f.tools, nil
}

func (f *fakeMCPTools) Call(_ context.Context, username, toolName, argsJSON string) (string, error) {
	f.callInvoked = true
	f.gotUsername = username
	f.gotToolName = toolName
	f.gotArgsJSON = argsJSON
	return f.callResult, f.callErr
}

const (
	testToolName  = "weather__get_forecast"
	testToolArgs  = `{"city":"Berlin"}`
	testToolReply = "sunny, 22C"
	toolMsgRole   = "tool"
)

func TestStreamRunsToolCallThenFinishes(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: testToolReply,
	}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "what's the weather", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if !mcp.callInvoked {
		t.Fatal("expected MCPTools.Call to be invoked")
	}
	if mcp.gotUsername != testUsername || mcp.gotToolName != testToolName || mcp.gotArgsJSON != testToolArgs {
		t.Fatalf("Call invoked with wrong args: user=%q tool=%q args=%q", mcp.gotUsername, mcp.gotToolName, mcp.gotArgsJSON)
	}

	var toolEvents []chat.ChatEvent
	for _, e := range sink.events {
		if e.Type == chat.EventTool {
			toolEvents = append(toolEvents, e)
		}
	}
	if len(toolEvents) != 2 || toolEvents[0].Status != "running" || toolEvents[1].Status != "done" {
		t.Fatalf("expected running then done tool events, got: %+v", toolEvents)
	}
	if toolEvents[0].Tool != testToolName || toolEvents[1].Tool != testToolName {
		t.Fatalf("tool events missing tool name: %+v", toolEvents)
	}
	if toolEvents[0].Arguments != testToolArgs {
		t.Fatalf("expected running tool event to carry arguments %q, got: %+v", testToolArgs, toolEvents[0])
	}
	if toolEvents[1].Arguments != "" {
		t.Fatalf("expected done tool event to omit arguments, got: %+v", toolEvents[1])
	}

	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if streamed.String() != testReply {
		t.Fatalf("final content not streamed: %q", streamed.String())
	}
	last := msgs.added[len(msgs.added)-1]
	if last.Role != model.MsgRoleAssistant || last.Content != testReply {
		t.Fatalf("final content not persisted: %+v", last)
	}

	if len(prov.gotMessages) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(prov.gotMessages))
	}
	secondCallMsgs := prov.gotMessages[1]
	var hasToolResult bool
	for _, m := range secondCallMsgs {
		if m.Role == toolMsgRole && m.ToolCallID == "call_1" && m.Content == testToolReply {
			hasToolResult = true
		}
	}
	if !hasToolResult {
		t.Fatalf("expected tool result message forwarded to provider: %+v", secondCallMsgs)
	}
}

func TestStreamToolCallErrorBecomesToolResult(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled: true,
		tools:   []provider.ToolDefinition{{Name: testToolName}},
		callErr: errors.New("tool exploded"),
	}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "what's the weather", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var toolEvents []chat.ChatEvent
	for _, e := range sink.events {
		if e.Type == chat.EventTool {
			toolEvents = append(toolEvents, e)
		}
	}
	if len(toolEvents) != 2 || toolEvents[1].Status != "error" {
		t.Fatalf("expected error status tool event, got: %+v", toolEvents)
	}

	secondCallMsgs := prov.gotMessages[1]
	var hasErrResult bool
	for _, m := range secondCallMsgs {
		if m.Role == toolMsgRole && strings.HasPrefix(m.Content, "error: ") {
			hasErrResult = true
		}
	}
	if !hasErrResult {
		t.Fatalf("expected error tool result forwarded to provider: %+v", secondCallMsgs)
	}
	// Stream still completes.
	if sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("expected stream to finish with done event, got: %+v", sink.events[len(sink.events)-1])
	}
}

func TestStreamMCPNilBehavesUnchanged(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, e := range sink.events {
		if e.Type == chat.EventTool {
			t.Fatalf("expected no tool events when mcp is nil, got: %+v", sink.events)
		}
	}
}

func TestStreamMCPDisabledBehavesUnchanged(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mcp := &fakeMCPTools{enabled: false}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, e := range sink.events {
		if e.Type == chat.EventTool {
			t.Fatalf("expected no tool events when mcp disabled, got: %+v", sink.events)
		}
	}
	if mcp.callInvoked {
		t.Fatal("Call should not be invoked when mcp disabled")
	}
}

// capturingToolsProvider records the tools it was asked to stream with, then
// returns plain content (no tool calls).
type capturingToolsProvider struct {
	reply    string
	gotTools []provider.ToolDefinition
}

func (p *capturingToolsProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	_ = onToken(p.reply)
	return p.reply, nil
}

func (p *capturingToolsProvider) StreamChatWithTools(_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	p.gotTools = req.Tools
	_ = onToken(p.reply)
	return provider.StreamResult{Content: p.reply}, nil
}

const testMCPMaxTools = 100

func manyToolDefs(n int) []provider.ToolDefinition {
	defs := make([]provider.ToolDefinition, n)
	for i := range defs {
		defs[i] = provider.ToolDefinition{Name: "tool_" + strconv.Itoa(i)}
	}
	return defs
}

func TestStreamCapsInjectedMCPTools(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &capturingToolsProvider{reply: testReply}
	mcp := &fakeMCPTools{enabled: true, tools: manyToolDefs(130)}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, MCPMaxTools: testMCPMaxTools},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "what's my schedule", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if len(prov.gotTools) != testMCPMaxTools {
		t.Fatalf("provider received %d tools, want capped at %d", len(prov.gotTools), testMCPMaxTools)
	}
}

func TestStreamSmallToolSetPassesThroughUncapped(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &capturingToolsProvider{reply: testReply}
	mcp := &fakeMCPTools{enabled: true, tools: manyToolDefs(3)}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, MCPMaxTools: testMCPMaxTools},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "what's my schedule", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if len(prov.gotTools) != 3 {
		t.Fatalf("provider received %d tools, want 3 (uncapped)", len(prov.gotTools))
	}
}

func TestStreamMaxIterationsStopsInfiniteToolLoop(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &alwaysToolProvider{toolName: testToolName}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: "ok",
	}
	const maxIter = 3
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, MCPMaxIterations: maxIter},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "loop forever", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// maxIter rounds of tool calls, plus one forced tool-free final call once
	// the iteration budget is exhausted.
	const wantCalls = maxIter + 1
	if prov.calls != wantCalls {
		t.Fatalf("expected provider called exactly %d times, got %d", wantCalls, prov.calls)
	}
	if sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("expected stream to finish with done event even after exhausting iterations, got: %+v", sink.events[len(sink.events)-1])
	}
}

// countingMCP records how many times Call is invoked and returns canned output.
type countingMCP struct {
	tools    []provider.ToolDefinition
	calls    int
	lastTool string
}

func (m *countingMCP) Enabled() bool { return true }
func (m *countingMCP) ToolsFor(context.Context, string) ([]provider.ToolDefinition, error) {
	return m.tools, nil
}
func (m *countingMCP) Call(_ context.Context, _, toolName, _ string) (string, error) {
	m.calls++
	m.lastTool = toolName
	return "ok-result", nil
}

func TestPreGateReturnsSkillWithoutCallingMCP(t *testing.T) {
	reg, err := skill.Load()
	if err != nil {
		t.Fatalf("skill.Load: %v", err)
	}
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mcp := &countingMCP{tools: []provider.ToolDefinition{{Name: "garmin__create_strength_workout"}}}
	prov := &toolThenContentProvider{
		toolName:   "garmin__create_strength_workout",
		toolArgs:   `{"name":"x","exercises":[]}`,
		finalReply: "done",
	}
	svc := chat.NewService(prov,
		chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp, Skills: reg})

	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "make a workout", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("pre-gate should not call MCP on first triggering call; calls=%d", mcp.calls)
	}
	var toolMsgContent string
	for _, ms := range prov.gotMessages {
		for _, m := range ms {
			if m.Role == toolMsgRole {
				toolMsgContent = m.Content
			}
		}
	}
	if !strings.Contains(toolMsgContent, "catalog") {
		t.Fatalf("gated tool message should carry the workout skill body; got: %s", toolMsgContent)
	}
}

func TestLoadSkillToolReturnsBody(t *testing.T) {
	reg, _ := skill.Load()
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mcp := &countingMCP{}
	prov := &toolThenContentProvider{
		toolName:   "kadence__load_skill",
		toolArgs:   `{"name":"memory"}`,
		finalReply: "ok",
	}
	svc := chat.NewService(prov,
		chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp, Skills: reg})

	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "hi", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("load_skill must be handled locally, not via MCP; calls=%d", mcp.calls)
	}
	var toolMsgContent string
	for _, ms := range prov.gotMessages {
		for _, m := range ms {
			if m.Role == toolMsgRole {
				toolMsgContent = m.Content
			}
		}
	}
	if !strings.Contains(toolMsgContent, "authoritative history") {
		t.Fatalf("load_skill should return the memory skill body; got: %s", toolMsgContent)
	}
}

// alwaysToolUntilNoTools returns a tool call whenever tools are offered, and
// streams finalReply once req.Tools is empty (the forced final call).
type alwaysToolUntilNoTools struct {
	toolName   string
	finalReply string
	calls      int
}

func (p *alwaysToolUntilNoTools) StreamChat(context.Context, provider.ChatRequest, provider.TokenFunc) (string, error) {
	return "", errors.New("unused")
}
func (p *alwaysToolUntilNoTools) StreamChatWithTools(_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	p.calls++
	if len(req.Tools) == 0 {
		_ = onToken(p.finalReply)
		return provider.StreamResult{Content: p.finalReply}, nil
	}
	return provider.StreamResult{ToolCalls: []provider.ToolCall{{ID: "c", Name: p.toolName, Arguments: "{}"}}}, nil
}

func TestToolLoopForcesFinalAnswerOnCapExhaustion(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mcp := &countingMCP{tools: []provider.ToolDefinition{{Name: "foo"}}}
	prov := &alwaysToolUntilNoTools{toolName: "foo", finalReply: "here is your summary"}
	svc := chat.NewService(prov,
		chat.ServiceConfig{Model: "m", MaxTokens: 32, MCPMaxIterations: 2},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 7, testUsername, "", "", "do it", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var lastAsst string
	for _, m := range msgs.added {
		if m.Role == model.MsgRoleAssistant {
			lastAsst = m.Content
		}
	}
	if lastAsst != "here is your summary" {
		t.Fatalf("expected forced final answer persisted, got %q", lastAsst)
	}
	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if !strings.Contains(streamed.String(), "summary") {
		t.Fatalf("final answer not streamed; got %q", streamed.String())
	}
}

// requestCredentialsProvider issues a kadence__request_credentials tool call
// first. If mcpToolName is set, on the second round it extracts the token
// for mcpFieldName from the request_credentials tool result (found in the
// prior round's messages) and issues an MCP tool call whose arguments embed
// that token verbatim. Otherwise (or on the round after the MCP call) it
// streams finalReply.
type requestCredentialsProvider struct {
	reqReason    string
	reqFields    string // raw JSON array of {name,label,secret}
	mcpToolName  string
	mcpFieldName string
	finalReply   string
	calls        int
	gotMessages  [][]provider.Message
}

const credsToolName = "kadence__request_credentials"
const testCredsCallID = "call_creds"
const testMCPCallID = "call_mcp"

func (p *requestCredentialsProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", errors.New("StreamChat should not be called when tools are in play")
}

func (p *requestCredentialsProvider) StreamChatWithTools(_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	p.gotMessages = append(p.gotMessages, req.Messages)
	p.calls++
	switch p.calls {
	case 1:
		args := `{"reason":"` + p.reqReason + `","fields":` + p.reqFields + `}`
		return provider.StreamResult{
			ToolCalls: []provider.ToolCall{{ID: testCredsCallID, Name: credsToolName, Arguments: args}},
		}, nil
	case 2:
		if p.mcpToolName != "" {
			token := p.tokenFromToolResult(req.Messages)
			args := `{"password":"` + token + `"}`
			return provider.StreamResult{
				ToolCalls: []provider.ToolCall{{ID: testMCPCallID, Name: p.mcpToolName, Arguments: args}},
			}, nil
		}
	}
	if err := onToken(p.finalReply); err != nil {
		return provider.StreamResult{}, err
	}
	return provider.StreamResult{Content: p.finalReply}, nil
}

// tokenFromToolResult extracts the token for p.mcpFieldName out of the
// request_credentials tool result message present in msgs.
func (p *requestCredentialsProvider) tokenFromToolResult(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role != toolMsgRole || m.ToolCallID != testCredsCallID {
			continue
		}
		idx := strings.Index(m.Content, "}")
		if idx == -1 {
			continue
		}
		var tokens map[string]string
		if err := json.Unmarshal([]byte(m.Content[:idx+1]), &tokens); err != nil {
			continue
		}
		return tokens[p.mcpFieldName]
	}
	return ""
}

const (
	testCredsReason    = "need garmin login"
	testCredsFieldName = "password"
)

// TestRequestCredentialsToolEmitsEventAndReturnsTokens verifies the
// request_credentials intercept: it emits a credentials_request SSE event
// (no values/tokens in it), and once a goroutine Submits values via the
// broker, the tool result delivered back to the provider carries TOKENS,
// never raw values.
func TestRequestCredentialsToolEmitsEventAndReturnsTokens(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	broker := secret.NewBroker()
	fields := `[{"name":"` + testCredsFieldName + `","label":"Password","secret":true}]`
	prov := &requestCredentialsProvider{reqReason: testCredsReason, reqFields: fields, finalReply: testReply}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs, Secrets: broker})

	sink := &syncCapturingSink{}
	submitted := make(chan struct{})
	go func() {
		// Wait for the credentials_request event to show up, then submit.
		for {
			for _, e := range sink.snapshot() {
				if e.Type == chat.EventCredentials && e.RequestID != "" {
					_ = broker.Submit(testUserID, e.RequestID, map[string]string{testCredsFieldName: "s3cr3t-value"})
					close(submitted)
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "log me into garmin", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	<-submitted

	events := sink.snapshot()
	var credsEvent *chat.ChatEvent
	for i := range events {
		if events[i].Type == chat.EventCredentials {
			credsEvent = &events[i]
		}
	}
	if credsEvent == nil {
		t.Fatal("expected a credentials_request event")
	}
	if credsEvent.Reason != testCredsReason {
		t.Fatalf("credsEvent.Reason = %q, want %q", credsEvent.Reason, testCredsReason)
	}
	if len(credsEvent.Fields) != 1 || credsEvent.Fields[0].Name != testCredsFieldName {
		t.Fatalf("credsEvent.Fields = %+v", credsEvent.Fields)
	}

	// The tool result forwarded to the provider must carry tokens, not values.
	secondCallMsgs := prov.gotMessages[1]
	var toolResultContent string
	for _, m := range secondCallMsgs {
		if m.Role == toolMsgRole && m.ToolCallID == testCredsCallID {
			toolResultContent = m.Content
		}
	}
	if toolResultContent == "" {
		t.Fatal("expected a tool result message for the request_credentials call")
	}
	if strings.Contains(toolResultContent, "s3cr3t-value") {
		t.Fatalf("tool result must never contain the raw secret value: %q", toolResultContent)
	}
	var tokens map[string]string
	// The tool result is expected to be a JSON object (possibly with a trailing
	// instruction) containing the token map; extract the JSON object prefix.
	if idx := strings.Index(toolResultContent, "}"); idx != -1 {
		_ = json.Unmarshal([]byte(toolResultContent[:idx+1]), &tokens)
	}
	tok, ok := tokens[testCredsFieldName]
	if !ok || !strings.HasPrefix(tok, "kadence_secret_") {
		t.Fatalf("expected a kadence_secret_ token for %q in tool result: %q", testCredsFieldName, toolResultContent)
	}
}

// TestRequestCredentialsSubstitutesAndRedacts verifies the full flow: a
// submitted secret's token, when included in a later MCP tool call's
// arguments, is substituted with the REAL value only in the argument JSON
// sent to the fake MCP server, while the SSE "tool" event Arguments and the
// role:"tool" message forwarded to the provider retain the placeholder token.
// A secret value echoed back in the tool result or in streamed text must be
// redacted to "[redacted]".
func TestRequestCredentialsSubstitutesAndRedacts(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	broker := secret.NewBroker()
	const secretValue = "s3cr3t-value"
	fields := `[{"name":"` + testCredsFieldName + `","label":"Password","secret":true}]`

	var reqID string

	mcp := &fakeMCPTools{enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}}}
	// callResult echoes the secret back, to verify redaction of tool results.
	mcp.callResult = "logged in as " + secretValue

	prov := &requestCredentialsProvider{
		reqReason: testCredsReason, reqFields: fields,
		mcpToolName:  testToolName,
		mcpFieldName: testCredsFieldName,
		finalReply:   "done, " + secretValue,
	}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp, Secrets: broker})

	sink := &syncCapturingSink{}
	go func() {
		for {
			for _, e := range sink.snapshot() {
				if e.Type == chat.EventCredentials && e.RequestID != "" {
					reqID = e.RequestID
					_ = broker.Submit(testUserID, reqID, map[string]string{testCredsFieldName: secretValue})
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "log me into garmin", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if !mcp.callInvoked {
		t.Fatal("expected MCP Call to be invoked")
	}
	if strings.Contains(mcp.gotArgsJSON, "kadence_secret_") {
		t.Fatalf("MCP call args should contain the REAL value, not the token: %q", mcp.gotArgsJSON)
	}
	if !strings.Contains(mcp.gotArgsJSON, secretValue) {
		t.Fatalf("MCP call args should contain the REAL secret value: %q", mcp.gotArgsJSON)
	}

	events := sink.snapshot()
	// The SSE "tool" running event Arguments for the MCP call must show the
	// placeholder token (or at least never the raw value).
	for _, e := range events {
		if e.Type == chat.EventTool && e.Tool == testToolName && e.Status == toolStatusRunningForTest {
			if strings.Contains(e.Arguments, secretValue) {
				t.Fatalf("SSE tool event Arguments leaked the raw secret: %q", e.Arguments)
			}
		}
	}

	// The role:"tool" message forwarded to the provider (for the MCP call)
	// must not contain the raw secret in its recorded arguments either, and
	// any secret echoed in the tool RESULT content must be redacted.
	for _, callMsgs := range prov.gotMessages {
		for _, m := range callMsgs {
			if m.Role == toolMsgRole && m.ToolCallID == testMCPCallID {
				if strings.Contains(m.Content, secretValue) {
					t.Fatalf("tool result message leaked the raw secret: %q", m.Content)
				}
				if !strings.Contains(m.Content, "[redacted]") {
					t.Fatalf("expected tool result to be redacted: %q", m.Content)
				}
			}
		}
	}

	// Streamed final content that echoes the secret must be redacted too.
	var streamed strings.Builder
	for _, e := range events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if strings.Contains(streamed.String(), secretValue) {
		t.Fatalf("streamed content leaked the raw secret: %q", streamed.String())
	}
	if !strings.Contains(streamed.String(), "[redacted]") {
		t.Fatalf("expected streamed content to contain [redacted]: %q", streamed.String())
	}

	// Persisted assistant message must also be redacted.
	last := msgs.added[len(msgs.added)-1]
	if strings.Contains(last.Content, secretValue) {
		t.Fatalf("persisted assistant message leaked the raw secret: %+v", last)
	}
}

// TestMCPErrorRedactsSecretBeforeLogging is a regression test for the
// MCP-error log path: when a tool call fails and the error text embeds the
// submitted secret value (e.g. a login tool echoing the invalid password
// back), the raw secret must never reach slog, the tool result, or the SSE
// stream — only the redacted "[redacted]" placeholder may appear.
func TestMCPErrorRedactsSecretBeforeLogging(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	broker := secret.NewBroker()
	const secretValue = "s3cr3t-value"
	fields := `[{"name":"` + testCredsFieldName + `","label":"Password","secret":true}]`

	mcp := &fakeMCPTools{enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}}}
	// The MCP tool server rejects the credential and echoes it back in the
	// error text, as a real login tool might ("invalid password 's3cr3t-value'").
	mcp.callErr = errors.New("invalid password '" + secretValue + "'")

	prov := &requestCredentialsProvider{
		reqReason: testCredsReason, reqFields: fields,
		mcpToolName:  testToolName,
		mcpFieldName: testCredsFieldName,
		finalReply:   "done, " + secretValue,
	}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp, Secrets: broker})

	// Swap slog's default handler for a text handler writing into a buffer,
	// so we can assert on exactly what got logged. Restore afterwards.
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	sink := &syncCapturingSink{}
	go func() {
		for {
			for _, e := range sink.snapshot() {
				if e.Type == chat.EventCredentials && e.RequestID != "" {
					_ = broker.Submit(testUserID, e.RequestID, map[string]string{testCredsFieldName: secretValue})
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "log me into garmin", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if !mcp.callInvoked {
		t.Fatal("expected MCP Call to be invoked")
	}

	// The raw secret must never appear in the captured logs.
	if strings.Contains(logBuf.String(), secretValue) {
		t.Fatalf("raw secret leaked into logs: %s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "[redacted]") {
		t.Fatalf("expected redacted placeholder in logs: %s", logBuf.String())
	}

	// The role:"tool" error result forwarded to the provider must be redacted.
	var foundToolResult bool
	for _, callMsgs := range prov.gotMessages {
		for _, m := range callMsgs {
			if m.Role == toolMsgRole && m.ToolCallID == testMCPCallID {
				foundToolResult = true
				if strings.Contains(m.Content, secretValue) {
					t.Fatalf("tool result message leaked the raw secret: %q", m.Content)
				}
				if !strings.Contains(m.Content, "[redacted]") {
					t.Fatalf("expected tool result to be redacted: %q", m.Content)
				}
			}
		}
	}
	if !foundToolResult {
		t.Fatal("expected an error tool result forwarded to the provider")
	}

	// Streamed content must never leak the raw secret either.
	events := sink.snapshot()
	var streamed strings.Builder
	for _, e := range events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if strings.Contains(streamed.String(), secretValue) {
		t.Fatalf("streamed content leaked the raw secret: %q", streamed.String())
	}

	// Persisted assistant message must not leak the raw secret.
	if len(msgs.added) > 0 {
		last := msgs.added[len(msgs.added)-1]
		if strings.Contains(last.Content, secretValue) {
			t.Fatalf("persisted assistant message leaked the raw secret: %+v", last)
		}
	}
}

const toolStatusRunningForTest = "running"

// TestRequestCredentialsToolNotOfferedWhenSecretsNil verifies the feature-off
// path: with Secrets nil, the request_credentials tool must not be offered
// and normal MCP dispatch is unaffected.
func TestRequestCredentialsToolNotOfferedWhenSecretsNil(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &capturingToolsProvider{reply: testReply}
	mcp := &fakeMCPTools{enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}}}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", "hi", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, td := range prov.gotTools {
		if td.Name == credsToolName {
			t.Fatalf("request_credentials tool must not be offered when Secrets is nil: %+v", prov.gotTools)
		}
	}

	// Normal dispatch (via a regular tool call) is unaffected: run one to be
	// sure runToolCall/dispatchTool still work with Secrets nil.
	prov2 := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	svc2 := chat.NewService(prov2, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})
	sink2 := &capturingSink{}
	if err := svc2.Stream(context.Background(), testUserID, testUsername, "", "", "what's the weather", sink2); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !mcp.callInvoked {
		t.Fatal("expected normal MCP dispatch to still work when Secrets is nil")
	}
}

func TestStreamTruncatesTitleASCII(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	// ASCII string with 70 characters → should be truncated to 60 runes.
	longASCII := strings.Repeat("a", 70)
	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", longASCII, sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if convs.created == nil {
		t.Fatal("expected a conversation to be created")
	}
	if len(convs.created.Title) != 60 {
		t.Fatalf("title length = %d, want 60 (runes)", len(convs.created.Title))
	}
	if convs.created.Title != strings.Repeat("a", 60) {
		t.Fatalf("title = %q, want 60 'a' characters", convs.created.Title)
	}
}

func TestStreamTruncatesTitleMultibyte(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	// String with emoji (multi-byte in UTF-8).
	// Create a string with 70 runes (all emoji) → should be truncated to 60 runes.
	longMultibyte := strings.Repeat("🎯", 70) // Fire emoji, 4 bytes each
	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", longMultibyte, sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if convs.created == nil {
		t.Fatal("expected a conversation to be created")
	}

	// Verify it's valid UTF-8
	if !utf8.ValidString(convs.created.Title) {
		t.Fatalf("title is not valid UTF-8: %q", convs.created.Title)
	}

	// Verify it's 60 runes (not bytes)
	runes := []rune(convs.created.Title)
	if len(runes) != 60 {
		t.Fatalf("title has %d runes, want 60", len(runes))
	}

	// Verify it's the correct content (60 fire emojis)
	if convs.created.Title != strings.Repeat("🎯", 60) {
		t.Fatalf("title = %q, want 60 fire emojis", convs.created.Title)
	}
}

func TestStreamKeepsTitleUnchangedWhenShort(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	// Short string with mixed ASCII and emoji.
	shortTitle := "Hello 👋 World"
	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, "", "", shortTitle, sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if convs.created == nil {
		t.Fatal("expected a conversation to be created")
	}

	// Short strings should be unchanged.
	if convs.created.Title != shortTitle {
		t.Fatalf("title = %q, want %q", convs.created.Title, shortTitle)
	}
}
