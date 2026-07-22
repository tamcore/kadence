package chat

import (
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/model"
)

// TestUnitPromptLine verifies unitPromptLine returns the imperial sentence
// only for "imperial", falling back to metric for anything else (including
// empty/unknown values).
func TestUnitPromptLine(t *testing.T) {
	if l := unitPromptLine("imperial"); !strings.Contains(l, "miles") || !strings.Contains(l, "min/mile") {
		t.Fatalf("imperial line = %q", l)
	}
	for _, u := range []string{"metric", "", "bogus"} {
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
	if !strings.Contains(s.systemPrompt("imperial"), "miles") {
		t.Fatal("imperial systemPrompt missing miles line")
	}
	if !strings.Contains(s.systemPrompt("metric"), "kilometers") {
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

	got, dropped := s.boundHistory(history, "system prompt", "new user text", 0)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(got) != len(history) {
		t.Fatalf("len(got) = %d, want %d (untouched)", len(got), len(history))
	}
}

// TestBoundHistoryKeepsFirstUserMessage verifies the very first user message
// is always retained even when the budget is tiny.
func TestBoundHistoryKeepsFirstUserMessage(t *testing.T) {
	s := &Service{contextBudget: 1} // tiny budget: only the mandatory first turn should survive
	history := make([]model.Message, 0, 20*2)
	for i := range 20 {
		history = append(history, turnMsgs(i, 200, 200)...)
	}

	got, dropped := s.boundHistory(history, "system prompt", "new user text", 0)
	if len(got) < 2 {
		t.Fatalf("len(got) = %d, want at least the first turn (2 messages)", len(got))
	}
	if got[0].Role != model.MsgRoleUser || got[0].Content != history[0].Content {
		t.Fatalf("got[0] = %+v, want the first user message %+v", got[0], history[0])
	}
	if dropped == 0 {
		t.Fatal("dropped = 0, want some turns dropped under a tiny budget")
	}
}

// TestBoundHistoryRespectsBudgetAndDropsOldestMiddle verifies that with a
// budget that fits the first turn plus only the newest turn, the returned
// history is exactly [first turn, newest turn] and the old middle turn is
// dropped whole (never split).
func TestBoundHistoryRespectsBudgetAndDropsOldestMiddle(t *testing.T) {
	// Each turn (user+assistant, 100 chars each) costs ~50 estimated tokens
	// (estBytesPerToken=4: 200 bytes/4 = 50).
	history := append(turnMsgs(0, 100, 100), turnMsgs(1, 100, 100)...)
	history = append(history, turnMsgs(2, 100, 100)...)

	// Budget: first turn (~50) + room for exactly one more turn (~50), not two.
	s := &Service{contextBudget: 110}

	got, dropped := s.boundHistory(history, "", "", 0)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2 (the whole middle turn)", dropped)
	}
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4 (first turn + newest turn)", len(got))
	}
	// First turn (turn 0) kept.
	if got[0].Content != history[0].Content || got[1].Content != history[1].Content {
		t.Fatalf("first turn not preserved: got %+v", got[:2])
	}
	// Newest turn (turn 2) kept, not the dropped middle turn (turn 1).
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
	got, _ := s.boundHistory(history, "", "", 0)

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
