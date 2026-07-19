package chat

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// ConversationStore is the conversation persistence the service needs.
type ConversationStore interface {
	Create(ctx context.Context, userID int64, title string) (model.Conversation, error)
	GetByID(ctx context.Context, id, userID int64) (model.Conversation, error)
}

// MessageStore is the message persistence the service needs.
type MessageStore interface {
	Add(ctx context.Context, conversationID int64, role, content string) (model.Message, error)
	ListByConversation(ctx context.Context, conversationID int64) ([]model.Message, error)
}

// MCPTools is the MCP tool-calling surface the chat service needs: the set
// of tools available to a user, and dispatching a tool call. Satisfied by
// *mcp.Registry.
type MCPTools interface {
	// Enabled reports whether any MCP servers are configured.
	Enabled() bool
	// ToolsFor returns the tool definitions available to the given user.
	ToolsFor(ctx context.Context, username string) ([]provider.ToolDefinition, error)
	// Call invokes a named tool with JSON-encoded arguments and returns its
	// (also JSON-ish/plain text) result.
	Call(ctx context.Context, username, toolName, argsJSON string) (string, error)
}

// ServiceConfig carries model params + system prompt.
type ServiceConfig struct {
	Model            string
	MaxTokens        int
	Temperature      float64
	SystemPrompt     string
	Timeout          time.Duration
	MCPMaxIterations int
}

const defaultMaxToolIterations = 5

const defaultSystemPrompt = "You are Kadence, a knowledgeable and encouraging endurance-sports coach. " +
	"Give practical, safe, evidence-based training guidance. Be concise and supportive."

const titleMaxLen = 60

// Service orchestrates a streaming chat turn.
type Service struct {
	provider      provider.Provider
	cfg           ServiceConfig
	convs         ConversationStore
	msgs          MessageStore
	guardrail     *Guardrail
	rag           *RAG
	mcp           MCPTools
	maxIterations int
}

// NewService constructs a chat Service. guardrail, rag, and mcp may be nil (disabled).
func NewService(
	p provider.Provider, cfg ServiceConfig, convs ConversationStore, msgs MessageStore,
	guardrail *Guardrail, rag *RAG, mcp MCPTools,
) *Service {
	maxIterations := cfg.MCPMaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxToolIterations
	}
	return &Service{
		provider: p, cfg: cfg, convs: convs, msgs: msgs,
		guardrail: guardrail, rag: rag, mcp: mcp, maxIterations: maxIterations,
	}
}

func (s *Service) systemPrompt() string {
	if s.cfg.SystemPrompt != "" {
		return s.cfg.SystemPrompt
	}
	return defaultSystemPrompt
}

// Stream runs one chat turn: resolve/create the conversation, persist the user
// message, stream the assistant reply (persisting it), emitting SSE events.
func (s *Service) Stream(ctx context.Context, userID int64, username string, conversationID int64, userText string, sink EventSink) error {
	if conversationID == 0 {
		title := userText
		if len(title) > titleMaxLen {
			title = title[:titleMaxLen]
		}
		c, err := s.convs.Create(ctx, userID, title)
		if err != nil {
			return s.fail(sink, "could not create conversation")
		}
		conversationID = c.ID
	} else {
		if _, err := s.convs.GetByID(ctx, conversationID, userID); err != nil {
			return s.fail(sink, "conversation not found")
		}
	}

	if err := sink.Send(ChatEvent{Type: EventMeta, ConversationID: conversationID}); err != nil {
		return err
	}
	_ = sink.Flush()

	history, err := s.msgs.ListByConversation(ctx, conversationID)
	if err != nil {
		return s.fail(sink, "could not load history")
	}
	userMsg, err := s.msgs.Add(ctx, conversationID, model.MsgRoleUser, userText)
	if err != nil {
		return s.fail(sink, "could not save message")
	}

	req := provider.ChatRequest{
		Model:       s.cfg.Model,
		MaxTokens:   s.cfg.MaxTokens,
		Temperature: s.cfg.Temperature,
	}
	req.Messages = append(req.Messages, provider.Message{Role: model.MsgRoleSystem, Content: s.systemPrompt()})
	for _, m := range history {
		req.Messages = append(req.Messages, provider.Message{Role: m.Role, Content: m.Content})
	}
	req.Messages = append(req.Messages, provider.Message{Role: "user", Content: userText})

	streamCtx := ctx
	if s.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		streamCtx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()
	}

	if refused, err := s.applyGuardrail(ctx, streamCtx, conversationID, req.Messages, sink); refused {
		return err
	}

	if s.rag != nil {
		contexts, queryEmb, rErr := s.rag.Retrieve(ctx, userID, userText)
		if rErr != nil {
			slog.Warn("rag retrieve failed, proceeding", "err", rErr, "conversation", conversationID)
		} else {
			if len(contexts) > 0 {
				var b strings.Builder
				b.WriteString("Relevant notes from earlier conversations with this user (use if helpful):\n")
				for _, c := range contexts {
					b.WriteString("- ")
					b.WriteString(c)
					b.WriteString("\n")
				}
				req.Messages = insertAfterSystem(req.Messages, provider.Message{Role: model.MsgRoleSystem, Content: b.String()})
			}
			if err := s.rag.Store(ctx, userID, conversationID, userMsg.ID, userText, queryEmb); err != nil {
				slog.Warn("rag store user chunk failed", "err", err)
			}
		}
	}

	if s.mcp != nil && s.mcp.Enabled() {
		tools, toolsErr := s.mcp.ToolsFor(ctx, username)
		if toolsErr != nil {
			slog.Warn("mcp tools list failed, proceeding", "err", toolsErr)
		} else {
			req.Tools = tools
		}
	}

	full, err := s.runToolLoop(ctx, streamCtx, conversationID, username, req, sink)
	if err != nil {
		return err
	}

	assistantMsg, err := s.msgs.Add(ctx, conversationID, model.MsgRoleAssistant, full)
	if err != nil {
		slog.Error("persist assistant message", "err", err)
	}

	if s.rag != nil && full != "" {
		if emb, embErr := s.rag.Embed(ctx, full); embErr != nil {
			slog.Warn("rag embed assistant failed", "err", embErr)
		} else if storeErr := s.rag.Store(ctx, userID, conversationID, assistantMsg.ID, full, emb); storeErr != nil {
			slog.Warn("rag store assistant chunk failed", "err", storeErr)
		}
	}

	if err := sink.Send(ChatEvent{Type: EventDone}); err != nil {
		return err
	}
	return sink.Flush()
}

// applyGuardrail classifies the conversation and, if it's off-topic, streams
// the refusal message, persists it, and sends EventDone. It reports
// refused=true when Stream should return immediately with the returned err
// (which may be nil). A classifier failure fails open (refused=false).
func (s *Service) applyGuardrail(
	ctx, streamCtx context.Context, conversationID int64, reqMessages []provider.Message, sink EventSink,
) (refused bool, err error) {
	if s.guardrail == nil {
		return false, nil
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
		return false, nil
	}
	if !offTopic {
		return false, nil
	}

	refusal := s.guardrail.RefusalMessage()
	_, _ = s.msgs.Add(ctx, conversationID, model.MsgRoleAssistant, refusal)
	_ = sink.Send(ChatEvent{Type: EventToken, Delta: refusal})
	_ = sink.Flush()
	_ = sink.Send(ChatEvent{Type: EventDone})
	return true, sink.Flush()
}

// runToolLoop streams the assistant reply, handling any MCP tool calls the
// model requests, up to s.maxIterations rounds. It returns the final
// tool-free assistant content (persistence and RAG-embedding happen in the
// caller).
func (s *Service) runToolLoop(
	ctx, streamCtx context.Context, conversationID int64, username string, req provider.ChatRequest, sink EventSink,
) (string, error) {
	maxIter := s.maxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxToolIterations
	}

	for i := 0; i < maxIter; i++ {
		result, streamErr := s.provider.StreamChatWithTools(streamCtx, req, func(delta string) error {
			if e := sink.Send(ChatEvent{Type: EventToken, Delta: delta}); e != nil {
				return e
			}
			return sink.Flush()
		})
		if streamErr != nil {
			slog.Error("chat stream failed", "err", streamErr, "conversation", conversationID)
			if result.Content != "" {
				_, _ = s.msgs.Add(ctx, conversationID, model.MsgRoleAssistant, result.Content)
			}
			return "", s.fail(sink, "the assistant could not complete the response")
		}
		if len(result.ToolCalls) == 0 {
			return result.Content, nil
		}

		req.Messages = append(req.Messages, provider.Message{
			Role: model.MsgRoleAssistant, Content: result.Content, ToolCalls: result.ToolCalls,
		})
		for _, tc := range result.ToolCalls {
			req.Messages = append(req.Messages, s.runToolCall(ctx, username, tc, sink))
		}
	}

	return "", nil
}

// runToolCall dispatches a single tool call through s.mcp, emitting
// running/done/error tool events on sink, and returns the resulting
// role:"tool" message to append to the provider request.
func (s *Service) runToolCall(ctx context.Context, username string, tc provider.ToolCall, sink EventSink) provider.Message {
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: "running"})
	_ = sink.Flush()

	out, cErr := s.mcp.Call(ctx, username, tc.Name, tc.Arguments)
	status := "done"
	if cErr != nil {
		slog.Warn("mcp tool call failed", "tool", tc.Name, "err", cErr)
		out = "error: " + cErr.Error()
		status = "error"
	}

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()

	return provider.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Name, Content: out}
}

// insertAfterSystem inserts m right after a leading system message, or
// prepends it when there is no system message at index 0.
func insertAfterSystem(msgs []provider.Message, m provider.Message) []provider.Message {
	if len(msgs) > 0 && msgs[0].Role == model.MsgRoleSystem {
		out := make([]provider.Message, 0, len(msgs)+1)
		out = append(out, msgs[0], m)
		return append(out, msgs[1:]...)
	}
	return append([]provider.Message{m}, msgs...)
}

func (s *Service) fail(sink EventSink, msg string) error {
	_ = sink.Send(ChatEvent{Type: EventError, Message: msg})
	_ = sink.Flush()
	return errors.New(msg)
}
