package chat

import (
	"context"
	"log/slog"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// requestTokenEstimate sums the estimated cost of every message in a provider
// request, using the same estimator assembleTurnContext bounds the turn with
// (the system prompt and RAG inserts are ordinary messages by this point). Tool
// definitions are excluded, matching the pre-loop accounting.
func requestTokenEstimate(messages []provider.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateProviderMessageTokens(message)
	}
	return total
}

// toolLoopReserveDivisor caps the output reserve the loop sheds down to at a
// quarter of the context budget, so a large MaxTokens cannot shed the whole
// conversation away.
const toolLoopReserveDivisor = 4

// shedTarget is the token ceiling the tool loop sheds down to: the context
// budget minus a reserve for the model's own output. assembleTurnContext fits
// the pre-loop request up TO the full budget, so shedding only back to the
// budget would breach again on the very next round.
func (s *Service) shedTarget() int {
	reserve := s.cfg.MaxTokens
	maxReserve := s.contextBudget / toolLoopReserveDivisor
	if reserve <= 0 || reserve > maxReserve {
		reserve = maxReserve
	}
	return s.contextBudget - reserve
}

// shedOldestContext drops the oldest sheddable messages — the RAG inserts and
// prior history that sit between the leading system prompt and the current user
// turn at keepFrom — until the request fits target, and returns the new slice
// plus how many messages went. The system prompt, the current turn and
// everything the tool loop appended are never dropped: the current turn is the
// question being answered, and a tool result must stay paired with the
// assistant message that requested it (see shedOldestToolRounds for those).
// RAG inserts go first because insertAfterSystem places them at the front;
// retrieved auxiliary context is the cheapest thing to lose.
func shedOldestContext(messages []provider.Message, keepFrom, target int) ([]provider.Message, int) {
	sheddable := keepFrom - 1
	if sheddable <= 0 {
		return messages, 0
	}
	used := requestTokenEstimate(messages)
	dropped := 0
	for dropped < sheddable && used > target {
		used -= estimateProviderMessageTokens(messages[1+dropped])
		dropped++
	}
	if dropped == 0 {
		return messages, 0
	}
	out := make([]provider.Message, 0, len(messages)-dropped)
	out = append(out, messages[0])
	out = append(out, messages[1+dropped:]...)
	return out, dropped
}

// shedOldestToolRounds drops whole leading tool rounds after keepFrom — an
// assistant message carrying tool calls plus every tool result answering it —
// until the request fits target or only the newest round is left, and returns
// the new slice plus how many messages went. Whole rounds only: a tool result
// separated from its assistant tool_calls message is rejected by providers. The
// newest round always survives, since it holds the evidence the final answer is
// meant to be based on.
func shedOldestToolRounds(messages []provider.Message, keepFrom, target int) ([]provider.Message, int) {
	var starts []int
	for i := keepFrom + 1; i < len(messages); i++ {
		if messages[i].Role == model.MsgRoleAssistant && len(messages[i].ToolCalls) > 0 {
			starts = append(starts, i)
		}
	}
	if len(starts) < 2 {
		return messages, 0
	}
	used := requestTokenEstimate(messages)
	rounds := 0
	for rounds < len(starts)-1 && used > target {
		for i := starts[rounds]; i < starts[rounds+1]; i++ {
			used -= estimateProviderMessageTokens(messages[i])
		}
		rounds++
	}
	if rounds == 0 {
		return messages, 0
	}
	dropped := starts[rounds] - starts[0]
	out := make([]provider.Message, 0, len(messages)-dropped)
	out = append(out, messages[:starts[0]]...)
	out = append(out, messages[starts[rounds]:]...)
	return out, dropped
}

// forceFinalAnswer makes one tool-free call so the user always receives a
// closing answer when the tool loop stops early — either because the iteration
// cap is spent or because the appended tool results no longer fit the context
// budget. Withdrawing the tools alone would re-send the same oversized request
// to a provider that already had reason to reject it, so an over-target request
// is shed first: oldest context, then whole oldest tool rounds. Continuation of
// a truncated answer still applies.
func (s *Service) forceFinalAnswer(
	streamCtx context.Context, conversationID string, userID int64,
	req provider.ChatRequest, redactor *turnRedactor, onToken provider.TokenFunc,
	state toolTurnState, keepFrom int,
) (string, toolTurnState, error) {
	req.Tools = nil
	if before := requestTokenEstimate(req.Messages); before > s.shedTarget() {
		messages, droppedContext := shedOldestContext(req.Messages, keepFrom, s.shedTarget())
		messages, droppedRounds := shedOldestToolRounds(messages, keepFrom-droppedContext, s.shedTarget())
		if droppedContext+droppedRounds > 0 {
			req.Messages = messages
			slog.Warn("shed context before the forced final answer",
				"conversation", conversationID,
				"dropped_context_messages", droppedContext, "dropped_tool_messages", droppedRounds,
				"estimated_tokens_before", before, "estimated_tokens_after", requestTokenEstimate(req.Messages),
				"budget_tokens", s.contextBudget)
		}
	}
	final, streamErr := s.provider.StreamChatWithTools(streamCtx, req, onToken)
	if streamErr != nil {
		slog.Error("final answer stream failed", "err", streamErr, "conversation", conversationID)
		return "", state, &providerStreamFailure{
			content: s.redactAssistantContent(final.Content, redactor, userID), err: streamErr,
		}
	}
	return s.completeIfTruncated(streamCtx, conversationID, req, final, onToken), state, nil
}
