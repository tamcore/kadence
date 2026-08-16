package chat

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tamcore/kadence/internal/mcpintent"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// resolveMCPAndSystemPrompt resolves the caller's MCP server snapshot (once,
// for reuse across the whole turn) and builds the system prompt, folding in
// any per-server tool-usage hints so they are counted by boundHistory's
// token sizing further down in Stream.
func (s *Service) resolveMCPAndSystemPrompt(ctx context.Context, uc UserContext) (MCPUserSnapshot, string) {
	var mcpSnap MCPUserSnapshot
	if s.toolCatalog != nil {
		snapshot, err := s.toolCatalog.SnapshotFor(ctx, uc.Username)
		if err != nil {
			slog.Warn("tool snapshot failed, proceeding", "err", err)
		} else {
			mcpSnap = snapshot
		}
	}

	systemPrompt := s.systemPrompt(uc)
	if mcpSnap != nil {
		if hints := mcpSnap.ToolHints(); len(hints) > 0 {
			systemPrompt += "\n\n" + strings.Join(hints, "\n")
		}
	}
	return mcpSnap, systemPrompt
}

func (s *Service) retrieveRAGContext(
	ctx context.Context,
	conversationID string,
	userID int64,
	userText string,
	documentIDs []int64,
) (TurnRetrieval, error) {
	if s.rag == nil {
		return TurnRetrieval{}, nil
	}
	if strings.TrimSpace(userText) == "" {
		return TurnRetrieval{}, nil
	}
	retrieval, err := s.rag.RetrieveTurn(ctx, userID, userText, documentIDs)
	if err != nil {
		slog.Warn("rag retrieve failed, proceeding", "err", err, "conversation", conversationID)
		return retrieval, err
	}
	return retrieval, nil
}

// buildRAGInserts uses only the budget left after system + current-turn
// content. Broad RAG and history skills are lower priority than current
// text, attachment/document context, and images.
func (s *Service) buildRAGInserts(contexts []string, availableTokens int) []provider.Message {
	if len(contexts) == 0 || availableTokens <= 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("Relevant notes from earlier conversations with this user (use if helpful):\n")
	for _, c := range contexts {
		b.WriteString("- ")
		b.WriteString(c)
		b.WriteString("\n")
	}
	inserts := make([]provider.Message, 0, 1)
	note := provider.Message{Role: model.MsgRoleSystem, Content: b.String()}
	used := estimateTokens(note.Content)
	if used > availableTokens {
		return nil
	}
	inserts = append(inserts, note)
	if s.skills != nil {
		for _, sk := range s.skills.ForHistory() {
			cost := estimateTokens(sk.Body)
			if used+cost > availableTokens {
				continue
			}
			inserts = append(inserts, provider.Message{
				Role: model.MsgRoleSystem, Content: sk.Body,
			})
			used += cost
		}
	}
	return inserts
}

func guardrailMessages(history []model.Message, currentText string) []provider.Message {
	messages := make([]provider.Message, 0, len(history)+1)
	for _, message := range history {
		messages = append(messages, provider.Message{
			Role: message.Role, Content: message.Content,
		})
	}
	return append(messages, provider.Message{Role: model.MsgRoleUser, Content: currentText})
}

func interactiveIntentContext(history []model.Message, userText string, historyWindow int) mcpintent.TrustedContext {
	request := strings.TrimSpace(userText)
	if request == "" {
		request = fileOnlyClassifierText
	}
	return mcpintent.TrustedContext{
		Request: request,
		History: trustedTextHistory(history, historyWindow),
	}
}

func (s *Service) interactiveIntentContext(history []model.Message, userText string) mcpintent.TrustedContext {
	historyWindow := interactiveIntentHistoryWindow
	if s != nil && s.cfg.GuardrailHistoryWindow > 0 {
		historyWindow = s.cfg.GuardrailHistoryWindow
	}
	return interactiveIntentContext(history, userText, historyWindow)
}

func trustedTextHistory(history []model.Message, historyWindow int) []provider.Message {
	trusted := make([]provider.Message, 0, len(history))
	for _, message := range history {
		if message.Role != model.MsgRoleUser && message.Role != model.MsgRoleAssistant {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		trusted = append(trusted, provider.Message{Role: message.Role, Content: message.Content})
	}
	if historyWindow > 0 && len(trusted) > historyWindow {
		trusted = trusted[len(trusted)-historyWindow:]
	}
	return trusted
}

// classifyGuardrail classifies raw text/history before any document extractor,
// embedder, or main provider is called. Classifier failure intentionally fails
// open.
func (s *Service) classifyGuardrail(
	streamCtx context.Context, conversationID string, reqMessages []provider.Message,
) bool {
	if s.guardrail == nil {
		return false
	}

	classifierMsgs := make([]provider.Message, 0, len(reqMessages))
	for _, m := range reqMessages {
		if m.Role == model.MsgRoleSystem {
			continue
		}
		classifierMsgs = append(classifierMsgs, m)
	}

	offTopic, gErr := s.guardrail.Classify(streamCtx, classifierMsgs)
	if gErr != nil {
		slog.Warn("guardrail classifier failed, proceeding", "err", gErr, "conversation", conversationID)
		return false
	}
	return offTopic
}

func (s *Service) persistGuardrailRefusal(
	ctx context.Context, conversationID string, expectedUser model.Message, sink EventSink,
) error {
	refusal := s.guardrail.RefusalMessage()
	saveCtx, cancel := assistantSaveContext(ctx)
	defer cancel()
	assistantMessage, saveErr := s.msgs.AddChatAssistantIfLatestUser(saveCtx, conversationID, expectedUser, refusal, nil, nil)
	if saveErr != nil {
		return s.fail(sink, "could not save response")
	}
	_ = sink.Send(ChatEvent{Type: EventToken, Delta: refusal})
	_ = sink.Flush()
	_ = sink.Send(ChatEvent{
		Type: EventDone, AssistantMessageID: assistantMessage.ID, AssistantContent: &refusal,
	})
	return sink.Flush()
}
