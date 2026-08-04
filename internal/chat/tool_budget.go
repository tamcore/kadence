package chat

import (
	"context"
	"log/slog"

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

// forceFinalAnswer makes one tool-free call so the user always receives a
// closing answer when the tool loop stops early — either because the iteration
// cap is spent or because the appended tool results no longer fit the context
// budget. Continuation of a truncated answer still applies.
func (s *Service) forceFinalAnswer(
	streamCtx context.Context, conversationID string, userID int64,
	req provider.ChatRequest, redactor *turnRedactor, onToken provider.TokenFunc,
	state toolTurnState,
) (string, toolTurnState, error) {
	req.Tools = nil
	final, streamErr := s.provider.StreamChatWithTools(streamCtx, req, onToken)
	if streamErr != nil {
		slog.Error("final answer stream failed", "err", streamErr, "conversation", conversationID)
		return "", state, &providerStreamFailure{
			content: s.redactAssistantContent(final.Content, redactor, userID), err: streamErr,
		}
	}
	return s.completeIfTruncated(streamCtx, conversationID, req, final, onToken), state, nil
}
