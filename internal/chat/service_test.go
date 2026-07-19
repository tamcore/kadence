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

func (f fakeProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := f.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
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
	testUsername  = "alice"
	testConvID    = 5
	testConvTitle = "test"
)

func TestStreamNewConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		convs, msgs, nil, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 7, testUsername, 0, "hi coach", sink); err != nil {
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
		convs, msgs, nil, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, testConvID, "hi coach", sink); err != nil {
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
		convs, msgs, nil, nil, nil)

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, testUsername, 99, "hi coach", sink)
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
		convs, msgs, nil, nil, nil)

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, testUsername, testConvID, "hi coach", sink)
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

func (p deadlineAssertingProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestStreamAppliesTimeout(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(deadlineAssertingProvider{t: t, reply: testReply},
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp,
			SystemPrompt: testSystemMsg, Timeout: testTimeout,
		},
		convs, msgs, nil, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, testConvID, "hi coach", sink); err != nil {
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
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	mainP := &recordingProvider{}
	guard := chat.NewGuardrail(&verdictProvider{verdict: "OFF_TOPIC"}, chat.GuardrailConfig{
		Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain, AllowedTopics: testGuardrailTopics,
		RefusalMessage: testGuardrailRefusal, HistoryWindow: 6,
	})
	svc := chat.NewService(mainP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, convs, msgs, guard, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 1, testUsername, 0, "what's the stock market doing?", sink); err != nil {
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
	svc := chat.NewService(mainP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, convs, msgs, guard, nil, nil)

	if err := svc.Stream(context.Background(), 1, testUsername, 0, "how many rest days?", &capturingSink{}); err != nil {
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

func TestStreamInjectsRAGContextAndStores(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	fc := &fakeChunks{search: []model.Chunk{{Content: "you prefer morning runs"}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32}, convs, msgs, nil, rag, nil)

	if err := svc.Stream(context.Background(), 7, testUsername, 0, "plan my week", &capturingSink{}); err != nil {
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
)

func TestStreamRunsToolCallThenFinishes(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: testToolReply,
	}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, convs, msgs, nil, nil, mcp)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, 0, "what's the weather", sink); err != nil {
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
		if m.Role == "tool" && m.ToolCallID == "call_1" && m.Content == testToolReply {
			hasToolResult = true
		}
	}
	if !hasToolResult {
		t.Fatalf("expected tool result message forwarded to provider: %+v", secondCallMsgs)
	}
}

func TestStreamToolCallErrorBecomesToolResult(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled: true,
		tools:   []provider.ToolDefinition{{Name: testToolName}},
		callErr: errors.New("tool exploded"),
	}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, convs, msgs, nil, nil, mcp)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, 0, "what's the weather", sink); err != nil {
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
		if m.Role == "tool" && strings.HasPrefix(m.Content, "error: ") {
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
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, convs, msgs, nil, nil, nil)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, 0, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, e := range sink.events {
		if e.Type == chat.EventTool {
			t.Fatalf("expected no tool events when mcp is nil, got: %+v", sink.events)
		}
	}
}

func TestStreamMCPDisabledBehavesUnchanged(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	mcp := &fakeMCPTools{enabled: false}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, convs, msgs, nil, nil, mcp)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, 0, "hi coach", sink); err != nil {
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

func TestStreamMaxIterationsStopsInfiniteToolLoop(t *testing.T) {
	convs := &fakeConvs{byID: map[int64]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &alwaysToolProvider{toolName: testToolName}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: "ok",
	}
	const maxIter = 3
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, MCPMaxIterations: maxIter},
		convs, msgs, nil, nil, mcp)

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, testUsername, 0, "loop forever", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if prov.calls != maxIter {
		t.Fatalf("expected provider called exactly %d times, got %d", maxIter, prov.calls)
	}
	if sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("expected stream to finish with done event even after exhausting iterations, got: %+v", sink.events[len(sink.events)-1])
	}
}
