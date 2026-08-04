package chat

import (
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// tokensWorth sizes text so estimateTokens (bytes/estBytesPerToken) yields
// exactly tokens, making the shed arithmetic below exact rather than approximate.
func tokensWorth(tokens int) string {
	return strings.Repeat("x", tokens*estBytesPerToken)
}

// loopRequest mirrors req.Messages as runToolLoop receives it:
// [system, RAG insert, history…, current turn]. 69 tokens total; keepFrom = 4.
func loopRequest() []provider.Message {
	return []provider.Message{
		{Role: model.MsgRoleSystem, Content: tokensWorth(10)},   // 0: system prompt
		{Role: model.MsgRoleSystem, Content: tokensWorth(20)},   // 1: RAG insert
		{Role: model.MsgRoleUser, Content: tokensWorth(30)},     // 2: old history
		{Role: model.MsgRoleAssistant, Content: tokensWorth(4)}, // 3: old history
		{Role: model.MsgRoleUser, Content: tokensWorth(5)},      // 4: current turn
	}
}

func toolRound(id string, resultTokens int) []provider.Message {
	return []provider.Message{
		{Role: model.MsgRoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "t"}}},
		{Role: toolMsgRole, ToolCallID: id, Name: "t", Content: tokensWorth(resultTokens)},
	}
}

func TestShedOldestContextDropsOldestFirstAndKeepsTheAnchors(t *testing.T) {
	messages := loopRequest()
	// A target of 40 needs the 20-token RAG insert and the 30-token oldest
	// history message gone (69 -> 19).
	shed, dropped := shedOldestContext(messages, 4, 40)

	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if got := requestTokenEstimate(shed); got > 40 {
		t.Fatalf("estimate after shedding = %d, want <= 40", got)
	}
	if len(shed) != 3 ||
		shed[0].Content != messages[0].Content ||
		shed[len(shed)-1].Content != messages[4].Content {
		t.Fatalf("system prompt and current turn must survive, got %d messages: %+v", len(shed), shed)
	}
	if requestTokenEstimate(messages) != 69 {
		t.Fatal("shedOldestContext mutated its input")
	}
}

func TestShedOldestContextStopsAtTheCurrentTurn(t *testing.T) {
	messages := loopRequest()
	// A target below system prompt + current turn cannot be met: everything
	// between them goes, and nothing more.
	shed, dropped := shedOldestContext(messages, 4, 1)

	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3 (every sheddable message)", dropped)
	}
	if len(shed) != 2 {
		t.Fatalf("kept %d messages, want 2 (system prompt + current turn)", len(shed))
	}
}

func TestShedOldestContextIsANoOpWithNothingToShed(t *testing.T) {
	messages := []provider.Message{
		{Role: model.MsgRoleSystem, Content: tokensWorth(10)},
		{Role: model.MsgRoleUser, Content: tokensWorth(500)},
	}
	shed, dropped := shedOldestContext(messages, 1, 5)

	if dropped != 0 || len(shed) != 2 {
		t.Fatalf("dropped = %d, kept %d messages, want 0 and 2", dropped, len(shed))
	}
}

func TestShedOldestToolRoundsDropsWholeRoundsAndKeepsTheNewest(t *testing.T) {
	messages := append(loopRequest(), toolRound("a", 100)...)
	messages = append(messages, toolRound("b", 100)...)
	messages = append(messages, toolRound("c", 100)...)

	shed, dropped := shedOldestToolRounds(messages, 4, 200)

	if dropped != 4 {
		t.Fatalf("dropped = %d messages, want 4 (the two oldest rounds, whole)", dropped)
	}
	if got := requestTokenEstimate(shed); got > 200 {
		t.Fatalf("estimate after shedding = %d, want <= 200", got)
	}
	// The surviving round must still be a paired assistant/tool sequence.
	kept := shed[len(shed)-2:]
	if len(kept[0].ToolCalls) != 1 || kept[0].ToolCalls[0].ID != "c" || kept[1].ToolCallID != "c" {
		t.Fatalf("newest round not kept intact: %+v", kept)
	}
	if len(shed) != len(loopRequest())+2 {
		t.Fatalf("kept %d messages, want %d", len(shed), len(loopRequest())+2)
	}
}

func TestShedOldestToolRoundsKeepsASingleRoundWhole(t *testing.T) {
	messages := append(loopRequest(), toolRound("a", 1000)...)

	shed, dropped := shedOldestToolRounds(messages, 4, 10)

	if dropped != 0 || len(shed) != len(messages) {
		t.Fatalf("a lone round must survive: dropped = %d, kept %d of %d", dropped, len(shed), len(messages))
	}
}

func TestShedTargetLeavesOutputHeadroomBelowTheBudget(t *testing.T) {
	cases := []struct {
		name      string
		budget    int
		maxTokens int
		want      int
	}{
		{name: "reserve is MaxTokens", budget: 1000, maxTokens: 100, want: 900},
		{name: "reserve capped at a quarter of the budget", budget: 1000, maxTokens: 900, want: 750},
		{name: "unset MaxTokens falls back to the cap", budget: 1000, maxTokens: 0, want: 750},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{contextBudget: tc.budget, cfg: ServiceConfig{MaxTokens: tc.maxTokens}}
			if got := s.shedTarget(); got != tc.want {
				t.Fatalf("shedTarget() = %d, want %d", got, tc.want)
			}
		})
	}
}
