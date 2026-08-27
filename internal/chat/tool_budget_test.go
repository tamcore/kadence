package chat_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// budgetToolProvider requests one tool call whenever tools are offered and
// answers with finalReply once they are withdrawn, recording every request.
type budgetToolProvider struct {
	finalReply   string
	calls        int
	toolsOffered []bool
	gotMessages  [][]provider.Message
}

func (p *budgetToolProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", nil
}

func (p *budgetToolProvider) StreamChatWithTools(
	_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	p.calls++
	p.toolsOffered = append(p.toolsOffered, len(req.Tools) > 0)
	p.gotMessages = append(p.gotMessages, req.Messages)
	if len(req.Tools) == 0 {
		if err := onToken(p.finalReply); err != nil {
			return provider.StreamResult{}, err
		}
		return provider.StreamResult{Content: p.finalReply}, nil
	}
	return provider.StreamResult{
		ToolCalls: []provider.ToolCall{{ID: testToolCallID, Name: testToolName, Arguments: "{}"}},
	}, nil
}

// TestStreamToolLoopStopsGrowingPastContextBudget pins the in-loop budget
// guard: one oversized tool result must end the loop with a forced tool-free
// final answer instead of appending further rounds and letting the provider
// reject the request.
func TestStreamToolLoopStopsGrowingPastContextBudget(t *testing.T) {
	const maxIter = 5
	prov := &budgetToolProvider{finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: strings.Repeat("x", 32<<10),
	}
	msgs := &fakeMsgs{}
	svc := chat.NewService(prov,
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens,
			MCPMaxIterations: maxIter, ContextBudgetTokens: 1000,
		},
		chat.Deps{Convs: &fakeConvs{byID: map[string]model.Conversation{}}, Msgs: msgs, MCP: mcp},
	)

	sink := &capturingSink{}
	if err := svc.Stream(
		t.Context(), testUserID, chat.UserContext{Username: testUsername}, "",
		"call the huge tool", sink,
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// One tool round, then the forced tool-free final call — not maxIter rounds.
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (one tool round then a forced final answer)", prov.calls)
	}
	if len(prov.toolsOffered) != 2 || !prov.toolsOffered[0] || prov.toolsOffered[1] {
		t.Fatalf("tools offered per call = %v, want [true false]", prov.toolsOffered)
	}
	if sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("stream did not finish cleanly: %+v", sink.events[len(sink.events)-1])
	}
	last := msgs.added[len(msgs.added)-1]
	if last.Role != model.MsgRoleAssistant || last.Content != testReply {
		t.Fatalf("final answer not persisted: %+v", last)
	}
}

// TestStreamToolLoopShedsContextAndKeepsLooping pins the shed-and-continue
// behaviour: when the appended tool results breach the budget but there is prior
// context to shed, the loop drops the oldest context and runs another tool round
// instead of collapsing the whole turn to a single round. The forced final call
// must then be a SMALLER request than the one that breached — withdrawing the
// tools while re-sending the same oversized messages would just be rejected
// again.
func TestStreamToolLoopShedsContextAndKeepsLooping(t *testing.T) {
	const (
		maxIter = 5
		budget  = 2000
	)
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
	}}
	msgs := &fakeMsgs{}
	// Three prior turns (~125 tokens per message) as sheddable history.
	for i := range 3 {
		msgs.added = append(msgs.added,
			model.Message{Role: model.MsgRoleUser, Content: strings.Repeat("u", 500) + strconv.Itoa(i)},
			model.Message{Role: model.MsgRoleAssistant, Content: strings.Repeat("a", 500) + strconv.Itoa(i)},
		)
	}
	oldestHistory := msgs.added[0].Content

	prov := &budgetToolProvider{finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled: true,
		tools:   []provider.ToolDefinition{{Name: testToolName}},
		// ~1300 tokens per result: one round fits, two do not.
		callResult: strings.Repeat("x", 5<<10),
	}
	svc := chat.NewService(prov,
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens, SystemPrompt: "sp",
			MCPMaxIterations: maxIter, ContextBudgetTokens: budget,
		},
		chat.Deps{Convs: convs, Msgs: msgs, MCP: mcp},
	)

	sink := &capturingSink{}
	if err := svc.Stream(
		t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID,
		"call the big tool", sink,
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Two tool rounds, then the forced tool-free answer — not one round.
	if len(prov.toolsOffered) != 3 || !prov.toolsOffered[0] || !prov.toolsOffered[1] || prov.toolsOffered[2] {
		t.Fatalf("tools offered per call = %v, want [true true false] (shed and keep looping)", prov.toolsOffered)
	}
	breaching, final := prov.gotMessages[1], prov.gotMessages[2]
	if len(final) >= len(breaching) {
		t.Fatalf("forced final request has %d messages, want fewer than the breaching %d", len(final), len(breaching))
	}
	for _, message := range final {
		if strings.Contains(message.Content, oldestHistory) {
			t.Fatalf("oldest history survived into the final request: %+v", final)
		}
	}
	// Shedding must keep the surviving tool result paired with its request.
	for i, message := range final {
		if message.Role != "tool" {
			continue
		}
		if i == 0 || len(final[i-1].ToolCalls) == 0 {
			t.Fatalf("tool result at %d is not preceded by its assistant tool_calls message: %+v", i, final)
		}
	}
	if sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("stream did not finish cleanly: %+v", sink.events[len(sink.events)-1])
	}
}

// TestStreamToolLoopKeepsMaxIterationsWhenWithinBudget guards the other
// direction: small tool results must not trip the budget guard, so the
// existing maxIter behaviour is unchanged.
func TestStreamToolLoopKeepsMaxIterationsWhenWithinBudget(t *testing.T) {
	const maxIter = 3
	prov := &budgetToolProvider{finalReply: testReply}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: testToolReply,
	}
	svc := chat.NewService(prov,
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens,
			MCPMaxIterations: maxIter, ContextBudgetTokens: 8000,
		},
		chat.Deps{Convs: &fakeConvs{byID: map[string]model.Conversation{}}, Msgs: &fakeMsgs{}, MCP: mcp},
	)

	if err := svc.Stream(
		t.Context(), testUserID, chat.UserContext{Username: testUsername}, "",
		"loop forever", &capturingSink{},
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if prov.calls != maxIter+1 {
		t.Fatalf("provider calls = %d, want %d (maxIter rounds plus a forced final answer)",
			prov.calls, maxIter+1)
	}
}

// silentToolProvider keeps requesting tools and then answers with nothing once
// they are withdrawn — the behaviour observed live when a turn spent all its
// iterations on tool calls.
type silentToolProvider struct{ calls int }

func (p *silentToolProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", nil
}

func (p *silentToolProvider) StreamChatWithTools(
	_ context.Context, req provider.ChatRequest, _ provider.TokenFunc,
) (provider.StreamResult, error) {
	p.calls++
	if len(req.Tools) == 0 {
		return provider.StreamResult{}, nil
	}
	return provider.StreamResult{
		ToolCalls: []provider.ToolCall{{ID: testToolCallID, Name: testToolName, Arguments: "{}"}},
	}, nil
}

// A turn that spends its whole tool budget must still tell the user what
// happened. Persisting an empty assistant message leaves them staring at a
// blank reply with no way to know the run was cut short.
func TestExhaustedToolBudgetExplainsItselfInsteadOfAnsweringBlank(t *testing.T) {
	// Arrange
	const maxIter = 2
	prov := &silentToolProvider{}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: testToolReply,
	}
	msgs := &fakeMsgs{}
	svc := chat.NewService(prov,
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens,
			MCPMaxIterations: maxIter, ContextBudgetTokens: 8000,
		},
		chat.Deps{Convs: &fakeConvs{byID: map[string]model.Conversation{}}, Msgs: msgs, MCP: mcp},
	)
	sink := &capturingSink{}

	// Act
	if err := svc.Stream(
		t.Context(), testUserID, chat.UserContext{Username: testUsername}, "",
		"audit everything", sink,
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Assert
	got := ""
	found := false
	for _, m := range msgs.added {
		if m.Role == model.MsgRoleAssistant {
			got, found = m.Content, true
		}
	}
	if !found {
		t.Fatal("no assistant message persisted")
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("persisted an empty assistant message; the turn must explain that its tool budget ran out")
	}
	if !strings.Contains(strings.ToLower(got), "tool") {
		t.Errorf("assistant message = %q, want it to mention the tool budget", got)
	}
}

// failingSink rejects the notice write, standing in for a client that
// disconnected mid-turn.
type failingSink struct{ capturingSink }

func (s *failingSink) Send(chat.ChatEvent) error { return errors.New("client gone") }

// A dropped connection must not be reported as a successful turn.
func TestExhaustedToolBudgetPropagatesASendFailure(t *testing.T) {
	// Arrange
	prov := &silentToolProvider{}
	mcp := &fakeMCPTools{
		enabled:    true,
		tools:      []provider.ToolDefinition{{Name: testToolName}},
		callResult: testToolReply,
	}
	svc := chat.NewService(prov,
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens,
			MCPMaxIterations: 2, ContextBudgetTokens: 8000,
		},
		chat.Deps{Convs: &fakeConvs{byID: map[string]model.Conversation{}}, Msgs: &fakeMsgs{}, MCP: mcp},
	)

	// Act
	err := svc.Stream(
		t.Context(), testUserID, chat.UserContext{Username: testUsername}, "",
		"audit everything", &failingSink{},
	)

	// Assert
	if err == nil {
		t.Fatal("Stream() error = nil, want the send failure surfaced rather than a silent success")
	}
}

// The two limits produce different notices: telling a user to make fewer tool
// calls when they actually overflowed the context is misleading advice.
func TestForcedAnswerNoticesNameTheLimitThatWasHit(t *testing.T) {
	// Arrange / Act
	iteration := chat.ForcedByIterationCapNotice()
	context := chat.ForcedByContextBudgetNotice()

	// Assert
	if iteration == context {
		t.Fatal("both limits produce the same notice; each must name what was actually hit")
	}
	if !strings.Contains(iteration, "tool-call budget") {
		t.Errorf("iteration notice = %q, want it to name the tool-call budget", iteration)
	}
	if !strings.Contains(context, "context") {
		t.Errorf("context notice = %q, want it to name the context limit", context)
	}
}
