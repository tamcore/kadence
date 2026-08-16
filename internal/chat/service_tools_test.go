package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/mcpaudit"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

func TestStreamDoneCarriesCanonicalAssistantContentAfterToolLoop(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	streamer := &preliminaryToolProvider{}
	mcp := &fakeMCPTools{enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}}}
	svc := chat.NewService(streamer,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	last := sink.events[len(sink.events)-1]
	if last.Type != chat.EventDone || last.AssistantContent == nil ||
		*last.AssistantContent != "canonical answer" {
		t.Fatalf("done event = %+v, want canonical assistant content", last)
	}
}

func TestStreamSystemPromptIncludesMCPHintWhenSet(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	mcpTools := &fakeMCPTools{enabled: true, hints: []string{"Tool guide: browser: use for current info"}}
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcpTools})

	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "what's the weather", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sys string
	for _, m := range captP.gotMessages {
		if m.Role == model.MsgRoleSystem {
			sys = m.Content
		}
	}
	if !strings.Contains(sys, "Tool guide: browser: use for current info") {
		t.Fatalf("system prompt should include the MCP hint line; got: %s", sys)
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

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "what's the weather", &capturingSink{}); err != nil {
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

func TestStreamAuditsRemoteMCPCallWithChatAndModel(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply}
	mcp := &fakeMCPTools{enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}}, callResult: testToolReply}
	auditStore := &chatAuditStore{}
	recorder := mcpaudit.NewRecorder(auditStore, slog.Default(), time.Now)
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
		Convs: convs, Msgs: msgs, MCP: mcp, Audit: recorder,
	})

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "audit this", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if auditStore.started.ActorUserID != testUserID || auditStore.started.ActorUsername != testUsername ||
		auditStore.started.ConversationID != testNewConvID || auditStore.started.Model != testModel ||
		auditStore.started.ToolName != testToolName || auditStore.started.Arguments != testToolArgs {
		t.Fatalf("started audit = %+v", auditStore.started)
	}
	if auditStore.finished.Status != model.MCPAuditStatusSucceeded || auditStore.finished.Result != testToolReply {
		t.Fatalf("finished audit = %+v", auditStore.finished)
	}
	if auditStore.startCount != 1 || auditStore.finishCount != 1 {
		t.Fatalf("audit lifecycle counts = start %d, finish %d; want one each", auditStore.startCount, auditStore.finishCount)
	}
}

func TestStreamToolCallUsesExternalDeadlineWithoutShorteningDurableAuditContext(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	prov := &toolThenContentProvider{
		toolName: testToolName, toolArgs: testToolArgs, finalReply: testReply,
	}
	mcp := &fakeMCPTools{
		enabled: true, tools: []provider.ToolDefinition{{Name: testToolName}},
		callResult: testToolReply,
	}
	auditStore := &chatAuditStore{}
	recorder := mcpaudit.NewRecorder(auditStore, slog.Default(), time.Now)
	svc := chat.NewService(prov,
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens,
			Timeout: 40 * time.Millisecond,
		},
		chat.Deps{
			Convs: convs, Msgs: msgs, MCP: mcp, Audit: recorder,
		},
	)

	if err := svc.Stream(
		context.Background(), testUserID, chat.UserContext{Username: testUsername}, "",
		"call the tool", &capturingSink{},
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !mcp.callHadDeadline ||
		mcp.callDeadlineRemaining <= 0 ||
		mcp.callDeadlineRemaining > 100*time.Millisecond {
		t.Fatalf(
			"MCP call deadline = present:%v remaining:%v, want shared short turn deadline",
			mcp.callHadDeadline, mcp.callDeadlineRemaining,
		)
	}
	if auditStore.startContextErr != nil ||
		auditStore.finishContextErr != nil ||
		auditStore.startDeadlineRemaining < time.Second {
		t.Fatalf(
			"audit persistence inherited external deadline: start_remaining=%v start_err=%v finish_err=%v",
			auditStore.startDeadlineRemaining,
			auditStore.startContextErr,
			auditStore.finishContextErr,
		)
	}
	// Like the audit writes above, the assistant save must not run under the
	// caller's short tool deadline. Its own generous deadline is expected.
	if len(msgs.assistantSaveHadDeadlines) != 1 ||
		msgs.assistantSaveContextErrors[0] != nil ||
		(msgs.assistantSaveHadDeadlines[0] && msgs.assistantSaveDeadlineIn[0] < time.Second) {
		t.Fatalf(
			"assistant persistence context = deadline:%v remaining:%v err:%v",
			msgs.assistantSaveHadDeadlines,
			msgs.assistantSaveDeadlineIn,
			msgs.assistantSaveContextErrors,
		)
	}
}

func TestStreamToolDiscoveryUsesExternalDeadline(t *testing.T) {
	mcp := &fakeMCPTools{
		enabled: true,
		tools:   []provider.ToolDefinition{{Name: testToolName}},
	}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens,
			Timeout: 40 * time.Millisecond,
		},
		chat.Deps{
			Convs: &fakeConvs{byID: map[string]model.Conversation{}},
			Msgs:  &fakeMsgs{}, MCP: mcp,
		},
	)

	if err := svc.Stream(
		context.Background(), testUserID, chat.UserContext{Username: testUsername}, "",
		"discover tools", &capturingSink{},
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !mcp.toolsHadDeadline ||
		mcp.toolsDeadlineRemaining <= 0 ||
		mcp.toolsDeadlineRemaining > 100*time.Millisecond {
		t.Fatalf(
			"MCP tool discovery deadline = present:%v remaining:%v, want shared short turn deadline",
			mcp.toolsHadDeadline, mcp.toolsDeadlineRemaining,
		)
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
			ToolCalls: []provider.ToolCall{{ID: testToolCallID, Name: p.toolName, Arguments: p.toolArgs}},
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

// fakeMCPTools is a canned MCPTools implementation for tests. SnapshotFor
// hands out a *fakeMCPSnapshot bound back to this fake, so tests can still
// assert on Call/ToolsFor invocations via the parent.
type fakeMCPTools struct {
	enabled                bool
	tools                  []provider.ToolDefinition
	hints                  []string
	callResult             string
	callErr                error
	gotUsername            string
	gotToolName            string
	gotArgsJSON            string
	callInvoked            bool
	callHadDeadline        bool
	callDeadlineRemaining  time.Duration
	toolsHadDeadline       bool
	toolsDeadlineRemaining time.Duration
	snapshotCalls          int
}

func (f *fakeMCPTools) Enabled() bool { return f.enabled }

func (f *fakeMCPTools) SnapshotFor(_ context.Context, username string) chat.MCPUserSnapshot {
	f.snapshotCalls++
	f.gotUsername = username
	return &fakeMCPSnapshot{parent: f}
}

// fakeMCPSnapshot is the per-turn snapshot fakeMCPTools.SnapshotFor returns.
type fakeMCPSnapshot struct {
	parent *fakeMCPTools
}

func (s *fakeMCPSnapshot) ToolsFor(ctx context.Context) ([]provider.ToolDefinition, error) {
	if deadline, ok := ctx.Deadline(); ok {
		s.parent.toolsHadDeadline = true
		s.parent.toolsDeadlineRemaining = time.Until(deadline)
	}
	return s.parent.tools, nil
}

func (s *fakeMCPSnapshot) Call(ctx context.Context, toolName, argsJSON string) (string, error) {
	s.parent.callInvoked = true
	s.parent.gotToolName = toolName
	s.parent.gotArgsJSON = argsJSON
	if deadline, ok := ctx.Deadline(); ok {
		s.parent.callHadDeadline = true
		s.parent.callDeadlineRemaining = time.Until(deadline)
	}
	return s.parent.callResult, s.parent.callErr
}

func (s *fakeMCPSnapshot) CallWithTransform(
	ctx context.Context, toolName, argsJSON string, transform chat.ArgumentTransform,
) (string, error) {
	if transform != nil {
		var err error
		argsJSON, err = transform(argsJSON)
		if err != nil {
			return "", err
		}
	}
	return s.Call(ctx, toolName, argsJSON)
}

func (s *fakeMCPSnapshot) ToolHints() []string {
	return s.parent.hints
}

const (
	testToolName   = "weather__get_forecast"
	testToolCallID = "call_1"
	testToolArgs   = `{"city":"Berlin"}`
	testToolReply  = "sunny, 22C"
	toolMsgRole    = "tool"
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "what's the weather", sink); err != nil {
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
	if len(toolEvents) != 2 || toolEvents[0].Status != "running" || toolEvents[1].Status != testToolStatusDone {
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
		if m.Role == toolMsgRole && m.ToolCallID == testToolCallID &&
			strings.Contains(m.Content, `"result":"`+testToolReply+`"`) {
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "what's the weather", sink); err != nil {
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
		if m.Role == toolMsgRole && strings.Contains(m.Content, `"result":"error: `) {
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "hi coach", sink); err != nil {
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "hi coach", sink); err != nil {
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

const (
	testMCPMaxTools         = 100
	testConvertPaceToolName = "kadence__convert_pace"
)

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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "what's my schedule", sink); err != nil {
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "what's my schedule", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	hasPaceTool := false
	for _, tool := range prov.gotTools {
		if tool.Name == testConvertPaceToolName {
			hasPaceTool = true
			break
		}
	}
	if len(prov.gotTools) != 4 || !hasPaceTool {
		t.Fatalf("provider tools = %+v, want 3 MCP tools plus pace converter", prov.gotTools)
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "loop forever", sink); err != nil {
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
	// SnapshotFor resolves the applicable MCP servers (env + per-user DB
	// query) once per turn; it must not be re-invoked on every iteration of
	// the tool loop.
	if mcp.snapshotCalls != 1 {
		t.Fatalf("SnapshotFor called %d times across the tool loop, want exactly 1", mcp.snapshotCalls)
	}
}

// countingMCP records how many times Call is invoked and returns canned
// output. SnapshotFor hands out a *countingMCPSnapshot bound back to this
// fake so tests can assert on Call invocations via the parent.
type countingMCP struct {
	tools    []provider.ToolDefinition
	calls    int
	lastTool string
}

func (m *countingMCP) Enabled() bool { return true }

func (m *countingMCP) SnapshotFor(context.Context, string) chat.MCPUserSnapshot {
	return &countingMCPSnapshot{parent: m}
}

// countingMCPSnapshot is the per-turn snapshot countingMCP.SnapshotFor returns.
type countingMCPSnapshot struct {
	parent *countingMCP
}

func (s *countingMCPSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	return s.parent.tools, nil
}

func (s *countingMCPSnapshot) Call(_ context.Context, toolName, _ string) (string, error) {
	s.parent.calls++
	s.parent.lastTool = toolName
	return "ok-result", nil
}

func (s *countingMCPSnapshot) CallWithTransform(
	ctx context.Context, toolName, argsJSON string, transform chat.ArgumentTransform,
) (string, error) {
	if transform != nil {
		var err error
		argsJSON, err = transform(argsJSON)
		if err != nil {
			return "", err
		}
	}
	return s.Call(ctx, toolName, argsJSON)
}

func (s *countingMCPSnapshot) ToolHints() []string {
	return nil
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
	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "do it", sink); err != nil {
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
