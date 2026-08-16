package chat

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/secret"
)

// providerStreamFailure preserves an already-received partial provider result
// so the caller can persist it with the same latest-user CAS as a completed
// response before reporting the error to the client.
type providerStreamFailure struct {
	content string
	err     error
}

func (e *providerStreamFailure) Error() string { return e.err.Error() }
func (e *providerStreamFailure) Unwrap() error { return e.err }

const scheduledPartialFallback = "I prepared the scheduling task drafts below, but could not finish the response."

// assistantSaveTimeout bounds a persistence write that runs on a context
// detached from the request's, so it cannot outlive a shutdown.
const assistantSaveTimeout = 5 * time.Second

// assistantSaveContext returns a context for persisting an assistant message.
// The common reason a turn ends early is that the client hung up, which cancels
// the request context; saving on it would fail exactly when the save matters,
// leaving a user message with no reply and nothing to regenerate from.
func assistantSaveContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), assistantSaveTimeout)
}

func (s *Service) persistPartialAssistantAndFail(
	ctx context.Context, conversationID string, userID int64, expectedUser model.Message, content string,
	state toolTurnState, sink EventSink,
) error {
	if content == "" {
		if len(state.Handoffs) == 0 {
			return s.fail(sink, "the assistant could not complete the response")
		}
		content = scheduledPartialFallback
	}
	saveCtx, cancel := assistantSaveContext(ctx)
	defer cancel()
	assistantMessage, err := s.msgs.AddChatAssistantIfLatestUser(saveCtx, conversationID, expectedUser, content, state.Calls, handoffIDs(state.Handoffs))
	if err != nil {
		slog.Error("persist partial assistant message", "err", err)
		s.cleanupScheduledDrafts(saveCtx, userID, state.Handoffs)
		return s.fail(sink, "the assistant could not complete the response")
	}
	return s.failWithAssistant(sink, "the assistant could not complete the response", assistantMessage)
}

// cleanupScheduledDrafts removes drafts a failed turn left behind. It derives
// its own context because every caller reaches it after a save failed, and that
// save may have failed by exhausting the very deadline it was given.
func (s *Service) cleanupScheduledDrafts(ctx context.Context, userID int64, artifacts []scheduled.ChatArtifact) {
	if s.scheduled == nil {
		return
	}
	ids := handoffIDs(artifacts)
	if len(ids) == 0 {
		return
	}
	cleanupCtx, cancel := assistantSaveContext(ctx)
	defer cancel()
	if err := s.scheduled.CleanupChatDrafts(cleanupCtx, userID, ids); err != nil {
		slog.Warn("cleanup scheduled chat drafts failed", "handoff_ids", ids, "error_class", fmt.Sprintf("%T", err))
	}
}

// runToolLoop streams the assistant reply, handling any MCP tool calls the
// model requests, up to s.maxIterations rounds. It returns the final
// tool-free assistant content (persistence and RAG-embedding happen in the
// caller).
type toolTurnState struct {
	Calls          []model.MessageToolCall
	Handoffs       []scheduled.ChatArtifact
	ScheduledCalls int
}

func (s *Service) runToolLoop(
	ctx, streamCtx context.Context, conversationID string, userID int64, uc UserContext,
	sourceUser model.Message, history []model.Message,
	mcpSnap MCPUserSnapshot,
	req provider.ChatRequest, redactor *turnRedactor, sink EventSink,
) (string, toolTurnState, error) {
	maxIter := s.maxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxToolIterations
	}

	// turnCalls records every tool the assistant invokes this turn (name +
	// redacted args) for the persisted audit trail on the assistant message.
	var state toolTurnState

	onToken := func(delta string) error {
		if s.secrets != nil {
			delta = secret.Redact(delta, redactor.snapshot(s.secrets, userID))
		}
		if e := sink.Send(ChatEvent{Type: EventToken, Delta: delta}); e != nil {
			return e
		}
		return sink.Flush()
	}

	// keepFrom is the index of the current user turn in req.Messages: at entry
	// the request is [system, RAG inserts…, history…, current turn], so
	// everything at 1..keepFrom-1 is sheddable context and everything after it
	// belongs to a tool round. It moves down as context is shed.
	keepFrom := len(req.Messages) - 1

	gated := make(map[string]bool)
	for i := 0; i < maxIter; i++ {
		result, streamErr := s.provider.StreamChatWithTools(streamCtx, req, onToken)
		if streamErr != nil {
			slog.Error("chat stream failed", "err", streamErr, "conversation", conversationID)
			return "", state, &providerStreamFailure{
				content: s.redactAssistantContent(result.Content, redactor, userID), err: streamErr,
			}
		}
		if len(result.ToolCalls) == 0 {
			return s.completeIfTruncated(streamCtx, conversationID, req, result, onToken), state, nil
		}

		req.Messages = append(req.Messages, provider.Message{
			Role: model.MsgRoleAssistant, Content: result.Content, ToolCalls: result.ToolCalls,
		})
		for _, tc := range result.ToolCalls {
			args := safeMCPArguments(tc.Arguments)
			if s.secrets != nil {
				args = secret.Redact(args, redactor.snapshot(s.secrets, userID))
			}
			state.Calls = append(state.Calls, model.MessageToolCall{Name: tc.Name, Arguments: args})
			req.Messages = append(req.Messages, s.dispatchToolWithTurn(
				ctx, streamCtx, conversationID, userID, uc, sourceUser, history, mcpSnap, tc, gated, &state, redactor, sink,
			))
		}

		// The context budget is fitted once before the loop, but every round
		// appends an assistant message plus one result per tool call. On a
		// breach, shed the oldest context (RAG inserts, then oldest history) down
		// to a target that leaves output headroom, and carry on: withdrawing the
		// tools without shedding would both re-send an oversized request and
		// collapse a multi-round tool sequence to a single round. Only when
		// there is nothing left to shed does the turn finish early.
		used := requestTokenEstimate(req.Messages)
		if used <= s.contextBudget {
			continue
		}
		var dropped int
		req.Messages, dropped = shedOldestContext(req.Messages, keepFrom, s.shedTarget())
		keepFrom -= dropped
		remaining := requestTokenEstimate(req.Messages)
		if remaining <= s.contextBudget {
			slog.Warn("tool loop exceeded context budget; shed oldest context and continued",
				"conversation", conversationID, "iteration", i+1,
				"estimated_tokens", used, "estimated_tokens_after", remaining,
				"dropped_messages", dropped, "budget_tokens", s.contextBudget)
			continue
		}
		slog.Warn("tool loop exceeded context budget; forcing a final answer",
			"conversation", conversationID, "iteration", i+1,
			"estimated_tokens", used, "estimated_tokens_after", remaining,
			"dropped_messages", dropped, "budget_tokens", s.contextBudget)
		return s.forceFinalAnswer(
			streamCtx, conversationID, userID, req, redactor, onToken, state, keepFrom,
			forcedByContextBudget,
		)
	}

	// Iteration budget exhausted with tools still pending. Make one final
	// tool-free call so the user always receives a closing answer instead of
	// an empty response.
	slog.Warn("tool loop hit iteration cap; forcing a final answer",
		"conversation", conversationID, "maxIter", maxIter)
	return s.forceFinalAnswer(
		streamCtx, conversationID, userID, req, redactor, onToken, state, keepFrom,
		forcedByIterationCap,
	)
}

func (s *Service) redactAssistantContent(content string, redactor *turnRedactor, userID int64) string {
	if s.secrets == nil {
		return content
	}
	return secret.Redact(content, redactor.snapshot(s.secrets, userID))
}

// maxContinuations bounds how many times a truncated (finish_reason=length)
// answer is auto-continued before we give up, so a pathological model can't
// loop forever. Each continuation is itself capped at the model's MaxTokens.
const maxContinuations = 3

// continuationPrompt nudges the model to resume a truncated answer without
// repeating what it already produced.
const continuationPrompt = "Continue your previous answer exactly where it was cut off. " +
	"Do not repeat any text you already wrote; resume mid-sentence if needed."

// completeIfTruncated returns first.Content, transparently continuing the
// answer when the model stopped because it hit the token cap
// (finish_reason=length). Continuation deltas stream through onToken just like
// the initial answer, so the client sees one seamless reply. Continuations run
// tool-free and are bounded by maxContinuations; a stream error mid-continuation
// keeps whatever was produced rather than failing the whole turn.
func (s *Service) completeIfTruncated(
	streamCtx context.Context, conversationID string,
	req provider.ChatRequest, first provider.StreamResult, onToken provider.TokenFunc,
) string {
	full := first.Content
	finish := first.FinishReason
	for cont := 0; finish == provider.FinishLength && cont < maxContinuations; cont++ {
		slog.Warn("llm response truncated at token cap; continuing",
			"conversation", conversationID, "continuation", cont+1)

		contReq := req
		contReq.Tools = nil // finishing text; no further tool calls
		msgs := make([]provider.Message, 0, len(req.Messages)+2)
		msgs = append(msgs, req.Messages...)
		msgs = append(msgs,
			provider.Message{Role: model.MsgRoleAssistant, Content: full},
			provider.Message{Role: model.MsgRoleUser, Content: continuationPrompt},
		)
		contReq.Messages = msgs

		next, err := s.provider.StreamChatWithTools(streamCtx, contReq, onToken)
		full += next.Content
		if err != nil {
			slog.Error("continuation stream failed; keeping partial answer",
				"err", err, "conversation", conversationID)
			return full
		}
		finish = next.FinishReason
	}
	if finish == provider.FinishLength {
		slog.Warn("llm response still truncated after continuation cap",
			"conversation", conversationID, "cap", maxContinuations)
	}
	return full
}
