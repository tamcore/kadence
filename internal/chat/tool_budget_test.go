package chat_test

import (
	"context"
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
		context.Background(), testUserID, chat.UserContext{Username: testUsername}, "",
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
		context.Background(), testUserID, chat.UserContext{Username: testUsername}, "",
		"loop forever", &capturingSink{},
	); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if prov.calls != maxIter+1 {
		t.Fatalf("provider calls = %d, want %d (maxIter rounds plus a forced final answer)",
			prov.calls, maxIter+1)
	}
}
