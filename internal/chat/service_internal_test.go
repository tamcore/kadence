package chat

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/mcpaudit"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	testFITServerOne    = "GARMIN1"
	testFITServerTwo    = "GARMIN2"
	testFITDownloadTool = "download_activity_file"
	testFITRemoteTool   = "garmin__download_activity_file"
	testFITGenericTool  = "download_fit"
	testFITBridgeOne    = "http://garmin1:8081"
	testFITBridgeTwo    = "http://garmin2:8081"
	testFITBobPassword  = "bob-pass"
	testFITAlias        = "garmin"
	testFITGlobalScope  = "GLOBAL"
	testFITAliceScope   = "USER_alice"
	testFITBobScope     = "USER_bob"
)

type fitToolSnapshot struct {
	tools      []provider.ToolDefinition
	callResult string
	callErr    error
	prefixes   map[string]string
	calledTool *string
}

func (s fitToolSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	return s.tools, nil
}

func (s fitToolSnapshot) Call(_ context.Context, toolName, _ string) (string, error) {
	if s.calledTool != nil {
		*s.calledTool = toolName
	}
	return s.callResult, s.callErr
}

func (fitToolSnapshot) ToolHints() []string { return nil }

func (s fitToolSnapshot) ServerPrefix(name, scope string) (string, bool) {
	prefix, ok := s.prefixes[name+"\x00"+scope]
	return prefix, ok
}

type fitEventSink struct{ events []ChatEvent }

func (s *fitEventSink) Send(event ChatEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (*fitEventSink) Flush() error { return nil }

type fitAuditStore struct {
	started  model.MCPAuditCall
	finished model.MCPAuditCall
}

func (s *fitAuditStore) Start(_ context.Context, call model.MCPAuditCall) (int64, error) {
	s.started = call
	return 12, nil
}

func (s *fitAuditStore) Finish(_ context.Context, id int64, status, result, errorText string, finishedAt time.Time) error {
	s.finished = model.MCPAuditCall{
		ID: id, Status: status, Result: result, Error: errorText, FinishedAt: &finishedAt,
	}
	return nil
}

func TestFITRoutesForSnapshotSelectsExactUserScopedMCP(t *testing.T) {
	s := NewService(nil, ServiceConfig{}, Deps{
		FITRoutes: []FITRoute{
			{
				ServerName: testFITServerOne, ServerScope: testFITAliceScope,
				DownloadTool: testFITDownloadTool, BridgeURL: testFITBridgeOne,
				BridgeAuthUser: "u", BridgeAuthPass: "alice-pass", MaxBytes: 1024,
			},
			{
				ServerName: testFITServerTwo, ServerScope: testFITBobScope,
				DownloadTool: testFITDownloadTool, BridgeURL: testFITBridgeTwo,
				BridgeAuthUser: "u", BridgeAuthPass: testFITBobPassword, MaxBytes: 1024,
			},
		},
	})

	routes := s.fitRoutesForSnapshot(fitToolSnapshot{prefixes: map[string]string{
		testFITServerTwo + "\x00" + testFITBobScope: testFITAlias,
	}})
	if len(routes) != 1 {
		t.Fatalf("len(routes) = %d, want only bob's route", len(routes))
	}
	if routes[0].source != testFITAlias || routes[0].downloadTool != testFITRemoteTool {
		t.Fatalf("resolved route = %+v, want bob's effective download tool", routes[0])
	}
}

func TestAssembleToolsOffersFITOnlyForVisibleUserRoute(t *testing.T) {
	s := NewService(nil, ServiceConfig{MCPMaxTools: 4}, Deps{
		FITRoutes: []FITRoute{
			{
				ServerName: testFITServerOne, ServerScope: testFITAliceScope,
				DownloadTool: testFITDownloadTool, BridgeURL: testFITBridgeOne,
				BridgeAuthUser: "u", BridgeAuthPass: "alice-pass", MaxBytes: 1024,
			},
			{
				ServerName: testFITServerTwo, ServerScope: testFITBobScope,
				DownloadTool: testFITDownloadTool, BridgeURL: testFITBridgeTwo,
				BridgeAuthUser: "u", BridgeAuthPass: testFITBobPassword, MaxBytes: 1024,
			},
		},
	})

	aliceTools := s.assembleTools(context.Background(), fitToolSnapshot{prefixes: map[string]string{
		testFITServerOne + "\x00" + testFITAliceScope: testFITAlias,
	}})
	if !hasToolNamed(aliceTools, analyzeGarminFITToolName) {
		t.Fatalf("alice tools = %+v, want native FIT tool", aliceTools)
	}

	otherTools := s.assembleTools(context.Background(), fitToolSnapshot{})
	if hasToolNamed(otherTools, analyzeGarminFITToolName) {
		t.Fatalf("unrelated user tools = %+v, FIT route leaked across scope", otherTools)
	}
}

func TestAssembleToolsRequiresSourceWhenMultipleFITRoutesAreVisible(t *testing.T) {
	s := NewService(nil, ServiceConfig{MCPMaxTools: 4}, Deps{
		FITRoutes: []FITRoute{
			{
				ServerName: testFITServerOne, ServerScope: testFITGlobalScope,
				DownloadTool: testFITDownloadTool, BridgeURL: "http://garmin1:8081",
				BridgeAuthUser: "u", BridgeAuthPass: "p1", MaxBytes: 1024,
			},
			{
				ServerName: testFITServerTwo, ServerScope: testFITAliceScope,
				DownloadTool: testFITDownloadTool, BridgeURL: testFITBridgeTwo,
				BridgeAuthUser: "u", BridgeAuthPass: "p2", MaxBytes: 1024,
			},
		},
	})
	tools := s.assembleTools(context.Background(), fitToolSnapshot{prefixes: map[string]string{
		testFITServerOne + "\x00" + testFITGlobalScope: testFITAlias,
		testFITServerTwo + "\x00" + testFITAliceScope:  "garmin2",
	}})

	var fitTool provider.ToolDefinition
	for _, tool := range tools {
		if tool.Name == analyzeGarminFITToolName {
			fitTool = tool
			break
		}
	}
	var schema struct {
		Properties struct {
			Source struct {
				Enum []string `json:"enum"`
			} `json:"source"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(fitTool.Parameters, &schema); err != nil {
		t.Fatalf("decode FIT tool schema: %v", err)
	}
	if !slices.Equal(schema.Properties.Source.Enum, []string{testFITAlias, "garmin2"}) ||
		!slices.Contains(schema.Required, "source") {
		t.Fatalf("FIT tool schema = %+v, want required source enum", schema)
	}
}

func TestFITAnalysisUsesVisibleUserRoute(t *testing.T) {
	auditStore := &fitAuditStore{}
	s := NewService(nil, ServiceConfig{Model: "coach"}, Deps{
		Audit: mcpaudit.NewRecorder(auditStore, nil, time.Now),
		FITRoutes: []FITRoute{{
			ServerName: testFITServerTwo, ServerScope: testFITBobScope,
			DownloadTool: testFITDownloadTool, BridgeURL: "http://127.0.0.1:1",
			BridgeAuthUser: "u", BridgeAuthPass: testFITBobPassword, MaxBytes: 1024,
		}},
	})
	var calledTool string
	snapshot := fitToolSnapshot{
		prefixes:   map[string]string{testFITServerTwo + "\x00" + testFITBobScope: testFITAlias},
		callResult: `{"path":"/data/fit/activity.fit"}`,
		calledTool: &calledTool,
	}

	msg := s.dispatchTool(
		context.Background(),
		context.Background(),
		"chat-id",
		7,
		"bob",
		snapshot,
		provider.ToolCall{ID: "call-1", Name: analyzeGarminFITToolName, Arguments: `{"activity_id":42}`},
		map[string]bool{},
		&turnRedactor{},
		&fitEventSink{},
	)

	if calledTool != testFITRemoteTool {
		t.Fatalf("called tool = %q, want bob's visible download tool", calledTool)
	}
	if unfencedToolResult(t, msg.Content) != fitAnalysisErrorMessage {
		t.Fatalf("tool result = %q, want safe decode failure", msg.Content)
	}
	if auditStore.started.ToolName != testFITRemoteTool ||
		auditStore.started.ConversationID != "chat-id" || auditStore.started.Model != "coach" ||
		auditStore.finished.Status != model.MCPAuditStatusSucceeded {
		t.Fatalf("nested FIT audit start=%+v finish=%+v", auditStore.started, auditStore.finished)
	}
}

// unfencedToolResult returns the result carried inside an
// <untrusted_context>-fenced tool message.
func unfencedToolResult(t *testing.T, content string) string {
	t.Helper()
	payload := strings.TrimSuffix(
		strings.TrimPrefix(content, untrustedContextOpen+"\n"), "\n"+untrustedContextClose,
	)
	var envelope untrustedToolResultEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatalf("unmarshal fenced tool result %q: %v", content, err)
	}
	return envelope.Result
}

func hasToolNamed(tools []provider.ToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestAssembleToolsReservesFITToolWithinCap(t *testing.T) {
	s := NewService(nil, ServiceConfig{MCPMaxTools: 2}, Deps{
		FITRoutes: []FITRoute{{
			ServerName: "ACTIVITY", ServerScope: testFITGlobalScope, DownloadTool: testFITGenericTool,
			BridgeURL: "http://bridge", BridgeAuthUser: "u", BridgeAuthPass: "p", MaxBytes: 1024,
		}},
	})
	tools := s.assembleTools(context.Background(), fitToolSnapshot{
		tools:    []provider.ToolDefinition{{Name: "activity__list"}},
		prefixes: map[string]string{"ACTIVITY\x00GLOBAL": "activity"},
	})

	if len(tools) != 2 ||
		!hasToolNamed(tools, analyzeGarminFITToolName) ||
		!hasToolNamed(tools, convertPaceToolName) ||
		hasToolNamed(tools, "activity__list") {
		t.Fatalf("tools = %+v, want native FIT and pace tools within cap", tools)
	}
}

func TestFITAnalysisReturnsSafeToolError(t *testing.T) {
	s := NewService(nil, ServiceConfig{}, Deps{
		FITRoutes: []FITRoute{{
			ServerName: "ACTIVITY", ServerScope: testFITGlobalScope, DownloadTool: testFITGenericTool,
			BridgeURL: "http://bridge", BridgeAuthUser: "u", BridgeAuthPass: "p", MaxBytes: 1024,
		}},
	})
	sink := &fitEventSink{}
	msg := s.handleFITAnalysis(
		context.Background(),
		fitToolSnapshot{
			callErr:  errors.New("sensitive path /data/fit/private.fit"),
			prefixes: map[string]string{"ACTIVITY\x00GLOBAL": "activity"},
		},
		provider.ToolCall{ID: "call-1", Name: analyzeGarminFITToolName, Arguments: `{"activity_id":42}`},
		sink,
	)

	if unfencedToolResult(t, msg.Content) != fitAnalysisErrorMessage {
		t.Fatalf("tool result = %q, want generic safe error", msg.Content)
	}
	if strings.Contains(msg.Content, "/data/fit") || len(sink.events) != 2 || sink.events[1].Status != toolStatusError {
		t.Fatalf("unsafe or incomplete error handling: msg=%q events=%+v", msg.Content, sink.events)
	}
}

// TestUnitPromptLine verifies unitPromptLine returns the imperial sentence
// only for "imperial", falling back to metric for anything else (including
// empty/unknown values).
func TestUnitPromptLine(t *testing.T) {
	if l := unitPromptLine(testImperialUnit); !strings.Contains(l, "miles") || !strings.Contains(l, "min/mile") {
		t.Fatalf("imperial line = %q", l)
	}
	for _, u := range []string{testMetricUnit, "", "bogus"} {
		l := unitPromptLine(u)
		if !strings.Contains(l, "kilometers") || !strings.Contains(l, "min/km") {
			t.Fatalf("unit %q line = %q, want metric", u, l)
		}
	}
}

// TestSystemPromptIncludesUnitLine verifies systemPrompt appends the correct
// unit-system sentence for the given unit preference.
func TestSystemPromptIncludesUnitLine(t *testing.T) {
	s := NewService(nil, ServiceConfig{}, Deps{})
	if !strings.Contains(s.systemPrompt(UserContext{UnitSystem: testImperialUnit}), "miles") {
		t.Fatal("imperial systemPrompt missing miles line")
	}
	if !strings.Contains(s.systemPrompt(UserContext{UnitSystem: "metric"}), "kilometers") {
		t.Fatal("metric systemPrompt missing km line")
	}
}

// turnMsgs builds a "user then assistant" turn's messages, as they would be
// loaded from ListByConversation (a real assistant reply persisted as a
// single row).
func turnMsgs(i int, userLen, assistantLen int) []model.Message {
	return []model.Message{
		{Role: model.MsgRoleUser, Content: strings.Repeat("u", userLen) + itoa(i)},
		{Role: model.MsgRoleAssistant, Content: strings.Repeat("a", assistantLen) + itoa(i)},
	}
}

func itoa(i int) string {
	digits := "0123456789"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i%10]) + out
		i /= 10
	}
	return out
}

// TestGroupHistoryTurnsPairsUserWithFollowingMessages verifies each stored
// user message starts a new turn and everything until the next user message
// (the assistant reply) stays attached to it.
func TestGroupHistoryTurnsPairsUserWithFollowingMessages(t *testing.T) {
	history := make([]model.Message, 0, 3*2)
	for i := range 3 {
		history = append(history, turnMsgs(i, 5, 5)...)
	}

	turns := groupHistoryTurns(history)
	if len(turns) != 3 {
		t.Fatalf("len(turns) = %d, want 3", len(turns))
	}
	for i, turn := range turns {
		if len(turn.messages) != 2 {
			t.Fatalf("turn %d has %d messages, want 2", i, len(turn.messages))
		}
		if turn.messages[0].Role != model.MsgRoleUser || turn.messages[1].Role != model.MsgRoleAssistant {
			t.Fatalf("turn %d roles = %+v, want [user, assistant]", i, turn.messages)
		}
	}
}

// TestBoundHistorySmallConversationUntouched verifies a conversation that
// fits comfortably within the budget is returned unchanged with zero dropped.
func TestBoundHistorySmallConversationUntouched(t *testing.T) {
	s := &Service{contextBudget: defaultContextBudgetTokens}
	history := make([]model.Message, 0, 3*2)
	for i := range 3 {
		history = append(history, turnMsgs(i, 10, 10)...)
	}

	got, dropped := s.boundHistory(
		history, "system prompt",
		provider.Message{Role: model.MsgRoleUser, Content: "new user text"}, 0,
	)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(got) != len(history) {
		t.Fatalf("len(got) = %d, want %d (untouched)", len(got), len(history))
	}
}

// TestBoundHistoryCanDropEveryHistoricalTurn verifies current-turn context
// takes priority even when retaining the first historical turn would exceed
// the budget.
func TestBoundHistoryCanDropEveryHistoricalTurn(t *testing.T) {
	s := &Service{contextBudget: 1}
	history := make([]model.Message, 0, 20*2)
	for i := range 20 {
		history = append(history, turnMsgs(i, 200, 200)...)
	}

	got, dropped := s.boundHistory(
		history, "system prompt",
		provider.Message{Role: model.MsgRoleUser, Content: "new user text"}, 0,
	)
	if len(got) != 0 {
		t.Fatalf("history = %+v, want all historical turns dropped", got)
	}
	if dropped != len(history) {
		t.Fatalf("dropped = %d, want %d", dropped, len(history))
	}
}

func TestFitCurrentTurnContextKeepsUnevenOrderedItemsWhenAggregateFits(t *testing.T) {
	const availableTokens = 200
	attachmentContent := "tiny attachment"
	documentContent := strings.Repeat("selected document ", 18)
	message := model.Message{Attachments: []model.MessageAttachment{{
		Filename:          "tiny.md",
		Kind:              model.AttachmentKindDocument,
		ExtractedMarkdown: attachmentContent,
	}}}
	documents := []model.Document{{
		ID:                42,
		Filename:          "selected.md",
		ExtractedMarkdown: documentContent,
	}}
	const envelopeOverhead = 128 + 2*96
	if len(attachmentContent)+len(documentContent) >
		availableTokens*estBytesPerToken-envelopeOverhead {
		t.Fatal("test setup: aggregate content must fit after envelope overhead")
	}

	fittedMessage, fittedDocuments := fitCurrentTurnContext(
		message, documents, nil, availableTokens,
	)

	if len(fittedMessage.Attachments) != 1 ||
		fittedMessage.Attachments[0].Filename != "tiny.md" ||
		fittedMessage.Attachments[0].ExtractedMarkdown != attachmentContent {
		t.Fatalf("ordered attachment = %+v", fittedMessage.Attachments)
	}
	if len(fittedDocuments) != 1 ||
		fittedDocuments[0].ID != 42 ||
		fittedDocuments[0].ExtractedMarkdown != documentContent {
		t.Fatalf("ordered document = %+v", fittedDocuments)
	}
}

func TestFitCurrentTurnContextBoundsActualEscapedEnvelope(t *testing.T) {
	const availableTokens = 96
	hostileFilename := strings.Repeat("\"<&", 60)
	hostileContent := strings.Repeat("<&", 120)
	message := model.Message{Attachments: []model.MessageAttachment{{
		Filename:          hostileFilename + ".md",
		Kind:              model.AttachmentKindDocument,
		ExtractedMarkdown: hostileContent,
	}}}
	documents := []model.Document{{
		ID: 7, Filename: hostileFilename + ".txt",
		ExtractedMarkdown: hostileContent,
	}}

	fittedMessage, fittedDocuments := fitCurrentTurnContext(
		message, documents, nil, availableTokens,
	)
	current, err := currentTurnProviderMessage(fittedMessage, fittedDocuments)
	if err != nil {
		t.Fatalf("currentTurnProviderMessage: %v", err)
	}
	if len(current.Content) > availableTokens*estBytesPerToken {
		t.Fatalf(
			"actual encoded current context uses %d bytes, want <= %d: %q",
			len(current.Content), availableTokens*estBytesPerToken, current.Content,
		)
	}
}

func TestFitCurrentTurnContextRedistributesUnusedShortSourceBudget(t *testing.T) {
	const availableTokens = 200
	shortContent := "short"
	firstRanked := strings.Repeat("first-ranked ", 15)
	secondRanked := strings.Repeat("second-ranked ", 15)
	message := model.Message{Attachments: []model.MessageAttachment{{
		Filename:          "short.md",
		Kind:              model.AttachmentKindDocument,
		ExtractedMarkdown: shortContent,
	}}}
	documents := []model.Document{{
		ID: 77, Filename: "long.md",
		ExtractedMarkdown: strings.Repeat("full document ", 100),
	}}
	sections := map[int64][]string{77: {firstRanked, secondRanked}}
	const equalSplitChars = (availableTokens*estBytesPerToken - 128 - 2*96) / 2

	fittedMessage, fittedDocuments := fitCurrentTurnContext(
		message, documents, sections, availableTokens,
	)

	if len(fittedMessage.Attachments) != 1 ||
		fittedMessage.Attachments[0].ExtractedMarkdown != shortContent {
		t.Fatalf("ordered short attachment = %+v", fittedMessage.Attachments)
	}
	if len(fittedDocuments) != 1 || fittedDocuments[0].ID != 77 {
		t.Fatalf("ordered selected document = %+v", fittedDocuments)
	}
	longContent := fittedDocuments[0].ExtractedMarkdown
	if len(longContent) <= equalSplitChars {
		t.Fatalf(
			"unused short-source budget was not redistributed: got %d bytes, equal split %d",
			len(longContent), equalSplitChars,
		)
	}
	if !strings.HasPrefix(longContent, firstRanked) ||
		!strings.Contains(longContent, secondRanked) ||
		!strings.Contains(longContent, contextTruncatedMarker) {
		t.Fatalf("ranked/truncated selected context = %q", longContent)
	}
}

// TestBoundHistoryRespectsBudgetAndKeepsNewestTurns verifies a constrained
// budget keeps the two newest complete turns and drops the oldest.
func TestBoundHistoryRespectsBudgetAndKeepsNewestTurns(t *testing.T) {
	// Each turn (user+assistant, 100 chars each) costs ~50 estimated tokens
	// (estBytesPerToken=4: 200 bytes/4 = 50).
	history := append(turnMsgs(0, 100, 100), turnMsgs(1, 100, 100)...)
	history = append(history, turnMsgs(2, 100, 100)...)

	// Budget has room for exactly two turns, not all three.
	s := &Service{contextBudget: 110}

	got, dropped := s.boundHistory(
		history, "", provider.Message{Role: model.MsgRoleUser}, 0,
	)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2 (the whole oldest turn)", dropped)
	}
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4 (two newest turns)", len(got))
	}
	if got[0].Content != history[2].Content || got[1].Content != history[3].Content {
		t.Fatalf("middle turn not preserved: got %+v", got[:2])
	}
	if got[2].Content != history[4].Content || got[3].Content != history[5].Content {
		t.Fatalf("newest turn not preserved: got %+v, want turn 2 %+v", got[2:], history[4:6])
	}
}

// TestBoundHistoryNeverSplitsATurn verifies that dropped turns are always
// dropped in full (even/odd message counts never straddle the kept/dropped
// boundary), which in particular means a persisted tool-call audit
// (assistant message with ToolCalls) is never separated from the user
// message that triggered it.
func TestBoundHistoryNeverSplitsATurn(t *testing.T) {
	history := []model.Message{
		{Role: model.MsgRoleUser, Content: strings.Repeat("x", 100)},
		{
			Role: model.MsgRoleAssistant, Content: strings.Repeat("y", 100),
			ToolCalls: []model.MessageToolCall{{Name: "some_tool", Arguments: `{"a":1}`}},
		},
	}
	for i := 1; i < 10; i++ {
		history = append(history, turnMsgs(i, 100, 100)...)
	}

	s := &Service{contextBudget: 200}
	got, _ := s.boundHistory(
		history, "", provider.Message{Role: model.MsgRoleUser}, 0,
	)

	if len(got)%2 != 0 {
		t.Fatalf("len(got) = %d, want even (whole turns only)", len(got))
	}
	for i := 0; i < len(got); i += 2 {
		if got[i].Role != model.MsgRoleUser {
			t.Fatalf("message %d role = %q, want user (turn boundary preserved)", i, got[i].Role)
		}
		if got[i+1].Role != model.MsgRoleAssistant {
			t.Fatalf("message %d role = %q, want assistant (paired with its user turn)", i+1, got[i+1].Role)
		}
	}
	// The first turn's assistant message must still carry its ToolCalls audit
	// intact if the first turn happens to be the one with tool calls.
	if got[1].ToolCalls != nil && len(got[1].ToolCalls) != 1 {
		t.Fatalf("first turn ToolCalls corrupted: %+v", got[1].ToolCalls)
	}
}

func TestEstimateProviderMessageTokensIncludesImageTransportCost(t *testing.T) {
	message := provider.Message{
		Content: "12345678",
		Images: []provider.ImageContent{{
			Data: make([]byte, 7),
		}},
	}

	if got, want := estimateProviderMessageTokens(message), 5; got != want {
		t.Fatalf("estimateProviderMessageTokens = %d, want %d", got, want)
	}
}

func TestBoundHistoryReservesCurrentImageTransportCost(t *testing.T) {
	history := []model.Message{
		{Role: model.MsgRoleUser, Content: strings.Repeat("u", 100)},
		{Role: model.MsgRoleAssistant, Content: strings.Repeat("a", 100)},
	}
	s := &Service{contextBudget: 60}

	textOnly, textDropped := s.boundHistory(
		history, "", provider.Message{Role: model.MsgRoleUser}, 0,
	)
	withImage, imageDropped := s.boundHistory(
		history, "",
		provider.Message{
			Role: model.MsgRoleUser,
			Images: []provider.ImageContent{{
				Data: make([]byte, 31),
			}},
		},
		0,
	)

	if textDropped != 0 || len(textOnly) != 2 {
		t.Fatalf("text-only history = %+v dropped=%d, want retained", textOnly, textDropped)
	}
	if imageDropped != 2 || len(withImage) != 0 {
		t.Fatalf("image history = %+v dropped=%d, want dropped turn", withImage, imageDropped)
	}
}

func TestSelectHistoricalAttachmentPayloadIDsBoundsNewestTurns(t *testing.T) {
	history := make([]model.Message, 10)
	for i := range history {
		history[i] = model.Message{
			ID: int64(i + 1), Role: model.MsgRoleUser,
			Attachments: []model.MessageAttachment{{
				Kind: model.AttachmentKindDocument, PayloadBytes: 4,
			}},
		}
	}

	got := selectHistoricalAttachmentPayloadIDs(history, 1_000)
	want := []int64{3, 4, 5, 6, 7, 8, 9, 10}
	if !slices.Equal(got, want) {
		t.Fatalf("selected IDs = %v, want %v", got, want)
	}
}

func TestSelectHistoricalAttachmentPayloadIDsHonorsByteBudget(t *testing.T) {
	history := []model.Message{
		{
			ID: 1, Role: model.MsgRoleUser,
			Attachments: []model.MessageAttachment{{
				Kind: model.AttachmentKindImage, PayloadBytes: 300,
			}},
		},
		{
			ID: 2, Role: model.MsgRoleUser,
			Attachments: []model.MessageAttachment{{
				Kind: model.AttachmentKindDocument, PayloadBytes: 400,
			}},
		},
	}

	got := selectHistoricalAttachmentPayloadIDs(history, 100)
	if !slices.Equal(got, []int64{2}) {
		t.Fatalf("selected IDs = %v, want [2]", got)
	}
}

func TestSelectHistoricalAttachmentPayloadIDsBoundsReferenceTurns(t *testing.T) {
	history := make([]model.Message, 10)
	for i := range history {
		history[i] = model.Message{
			ID: int64(i + 1), Role: model.MsgRoleUser,
			DocumentReferences: []model.MessageDocumentReference{{
				DocumentID: new(int64), Available: true, PayloadBytes: 4,
			}},
		}
	}

	got := selectHistoricalAttachmentPayloadIDs(history, 1_000)
	want := []int64{3, 4, 5, 6, 7, 8, 9, 10}
	if !slices.Equal(got, want) {
		t.Fatalf("selected reference IDs = %v, want %v", got, want)
	}
}
