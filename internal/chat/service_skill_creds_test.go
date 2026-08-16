package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/chat/skill"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/secret"
)

func TestDefaultSystemPromptIsSlimAndPointsToSkills(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	captP := &capturingProvider{reply: "ok"}
	svc := chat.NewService(captP, chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs})
	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "hi", &capturingSink{}); err != nil {
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
	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "plan my week", &capturingSink{}); err != nil {
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
	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "hi", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, m := range captP.gotMessages {
		if m.Role == model.MsgRoleSystem && strings.Contains(m.Content, "authoritative history") {
			t.Fatal("memory skill must not be injected when there are no RAG notes")
		}
	}
}

func TestPreGateReturnsSkillWithoutCallingMCP(t *testing.T) {
	reg, err := skill.Load()
	if err != nil {
		t.Fatalf("skill.Load: %v", err)
	}
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	mcp := &countingMCP{tools: []provider.ToolDefinition{{Name: testStrengthWorkoutTool}}}
	prov := &toolThenContentProvider{
		toolName:   testStrengthWorkoutTool,
		toolArgs:   `{"name":"x","exercises":[]}`,
		finalReply: testToolStatusDone,
	}
	svc := chat.NewService(prov,
		chat.ServiceConfig{Model: "m", MaxTokens: 32},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp, Skills: reg})

	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "make a workout", &capturingSink{}); err != nil {
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

func TestPreGateSanitizesToolArgumentsWithoutChangingProviderContinuation(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments string
		wantSafe  string
	}{
		{
			name:      "intent object",
			arguments: `{"name":"x","_kadence_intent":"Create the requested workout"}`,
			wantSafe:  `{"name":"x"}`,
		},
		{
			name:      "non-object",
			arguments: `[{"_kadence_intent":"Create the requested workout"}]`,
			wantSafe:  `{}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reg, err := skill.Load()
			if err != nil {
				t.Fatalf("skill.Load: %v", err)
			}
			convs := &fakeConvs{byID: map[string]model.Conversation{}}
			msgs := &fakeMsgs{}
			mcp := &countingMCP{tools: []provider.ToolDefinition{{Name: testStrengthWorkoutTool}}}
			prov := &toolThenContentProvider{
				toolName: testStrengthWorkoutTool, toolArgs: test.arguments, finalReply: testToolStatusDone,
			}
			sink := &capturingSink{}
			svc := chat.NewService(prov,
				chat.ServiceConfig{Model: "m", MaxTokens: 32},
				chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp, Skills: reg})

			if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "make a workout", sink); err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if mcp.calls != 0 {
				t.Fatalf("pre-gate called MCP %d times", mcp.calls)
			}
			var runningArguments string
			for _, event := range sink.events {
				if event.Type == chat.EventTool && event.Tool == prov.toolName && event.Status == "running" {
					runningArguments = event.Arguments
					break
				}
			}
			if runningArguments != test.wantSafe {
				t.Fatalf("running arguments=%q want %q", runningArguments, test.wantSafe)
			}
			last := msgs.added[len(msgs.added)-1]
			if len(last.ToolCalls) != 1 || last.ToolCalls[0].Arguments != test.wantSafe {
				t.Fatalf("persisted tool calls=%+v want arguments %q", last.ToolCalls, test.wantSafe)
			}
			var continuationArguments string
			for _, message := range prov.gotMessages[len(prov.gotMessages)-1] {
				if len(message.ToolCalls) == 1 && message.ToolCalls[0].Name == prov.toolName {
					continuationArguments = message.ToolCalls[0].Arguments
				}
			}
			if continuationArguments != test.arguments {
				t.Fatalf("continuation arguments=%q want original %q", continuationArguments, test.arguments)
			}
		})
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

	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "hi", &capturingSink{}); err != nil {
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

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "log me into garmin", sink); err != nil {
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

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "log me into garmin", sink); err != nil {
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

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "log me into garmin", sink); err != nil {
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
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "hi", sink); err != nil {
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
	if err := svc2.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", "what's the weather", sink2); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !mcp.callInvoked {
		t.Fatal("expected normal MCP dispatch to still work when Secrets is nil")
	}
}

// TestStreamBudgetAccountsForRAGAndSkillInserts verifies that a large RAG
// context plus the skills it triggers are reserved against the token budget
// before history is bounded (see the boundHistory doc comment on
// reservedTokens: these inserts are mandatory, like the system prompt, so
// they shrink the allowance left for history rather than being counted only
// after the fact). Regression test: previously boundHistory sized the
// budget against systemPrompt+userText+history alone, so the RAG context and
// skill bodies inserted afterward via insertAfterSystem could push the
// actual provider request past ContextBudgetTokens whenever RAG hit or
// skills attached.
func TestStreamBudgetAccountsForRAGAndSkillInserts(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	// 3 turns of ~500 chars/side (~250 estimated tokens each), same shape as
	// TestStreamBoundsHistoryToContextBudget.
	for i := range 3 {
		msgs.added = append(msgs.added,
			model.Message{Role: model.MsgRoleUser, Content: strings.Repeat("u", 500) + strconv.Itoa(i)},
			model.Message{Role: model.MsgRoleAssistant, Content: strings.Repeat("a", 500) + strconv.Itoa(i)},
		)
	}
	firstUserContent := msgs.added[0].Content
	middleTurnUserContent := msgs.added[2].Content
	newestTurnUserContent := msgs.added[4].Content

	// A large RAG note (~800 chars => ~219 estimated tokens) plus the memory
	// skill it triggers (~377-byte body => ~94 estimated tokens) reserve
	// ~313 tokens against the budget before history is bounded.
	fc := &fakeChunks{search: []model.Chunk{{Content: strings.Repeat("n", 800)}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	reg, err := skill.Load()
	if err != nil {
		t.Fatalf("skill.Load: %v", err)
	}

	captP := &capturingProvider{reply: "ok"}
	// Budget fits system (including the always-on weather nudge line) + live
	// user + RAG/skill reserve + recent history, but not all three turns.
	// The oldest and middle turns must be dropped once the RAG/skill reserve
	// is accounted for. Under the old
	// (buggy) accounting — no reserve — all 3 turns would fit and the final
	// request (with RAG+skill inserts added after) would overshoot the budget.
	const budget = 960
	svc := chat.NewService(captP,
		chat.ServiceConfig{Model: "m", MaxTokens: 32, SystemPrompt: "sp", ContextBudgetTokens: budget},
		chat.Deps{Convs: convs, Msgs: msgs, RAG: rag, Skills: reg})

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "new question", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var totalTokens int
	var full strings.Builder
	for _, m := range captP.gotMessages {
		totalTokens += len(m.Content) / 4 // mirrors the len/4 estimateTokens heuristic
		full.WriteString(m.Content)
		full.WriteString("\n")
	}
	if totalTokens > budget {
		t.Fatalf("total estimated request tokens = %d, want <= budget (%d); messages: %+v", totalTokens, budget, captP.gotMessages)
	}

	got := full.String()
	if strings.Contains(got, firstUserContent) {
		t.Fatalf("expected oldest user message dropped, got messages: %+v", captP.gotMessages)
	}
	if !strings.Contains(got, newestTurnUserContent) {
		t.Fatalf("expected newest turn retained, got messages: %+v", captP.gotMessages)
	}
	if strings.Contains(got, middleTurnUserContent) {
		t.Fatalf("expected middle turn dropped once RAG/skill reserve is accounted for, got messages: %+v", captP.gotMessages)
	}
	if !strings.Contains(got, strings.Repeat("n", 800)) {
		t.Fatalf("expected RAG note injected, got messages: %+v", captP.gotMessages)
	}
	if !strings.Contains(got, "authoritative history") {
		t.Fatalf("expected memory skill injected alongside RAG notes, got messages: %+v", captP.gotMessages)
	}
}
