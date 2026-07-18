package chat

import (
	"context"
	"errors"
	"log/slog"
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

// ServiceConfig carries model params + system prompt.
type ServiceConfig struct {
	Model        string
	MaxTokens    int
	Temperature  float64
	SystemPrompt string
	Timeout      time.Duration
}

const defaultSystemPrompt = "You are Kadence, a knowledgeable and encouraging endurance-sports coach. " +
	"Give practical, safe, evidence-based training guidance. Be concise and supportive."

const titleMaxLen = 60

// Service orchestrates a streaming chat turn.
type Service struct {
	provider  provider.Provider
	cfg       ServiceConfig
	convs     ConversationStore
	msgs      MessageStore
	guardrail *Guardrail
}

// NewService constructs a chat Service. guardrail may be nil (disabled).
func NewService(p provider.Provider, cfg ServiceConfig, convs ConversationStore, msgs MessageStore, guardrail *Guardrail) *Service {
	return &Service{provider: p, cfg: cfg, convs: convs, msgs: msgs, guardrail: guardrail}
}

func (s *Service) systemPrompt() string {
	if s.cfg.SystemPrompt != "" {
		return s.cfg.SystemPrompt
	}
	return defaultSystemPrompt
}

// Stream runs one chat turn: resolve/create the conversation, persist the user
// message, stream the assistant reply (persisting it), emitting SSE events.
func (s *Service) Stream(ctx context.Context, userID, conversationID int64, userText string, sink EventSink) error {
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
	if _, err := s.msgs.Add(ctx, conversationID, model.MsgRoleUser, userText); err != nil {
		return s.fail(sink, "could not save message")
	}

	req := provider.ChatRequest{
		Model:       s.cfg.Model,
		MaxTokens:   s.cfg.MaxTokens,
		Temperature: s.cfg.Temperature,
	}
	req.Messages = append(req.Messages, provider.Message{Role: "system", Content: s.systemPrompt()})
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

	if s.guardrail != nil {
		classifierMsgs := make([]provider.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			if m.Role == model.MsgRoleSystem {
				continue
			}
			classifierMsgs = append(classifierMsgs, m)
		}
		offTopic, gErr := s.guardrail.Classify(streamCtx, classifierMsgs)
		switch {
		case gErr != nil:
			slog.Warn("guardrail classifier failed, proceeding", "err", gErr, "conversation", conversationID)
		case offTopic:
			refusal := s.guardrail.RefusalMessage()
			_, _ = s.msgs.Add(ctx, conversationID, model.MsgRoleAssistant, refusal)
			_ = sink.Send(ChatEvent{Type: EventToken, Delta: refusal})
			_ = sink.Flush()
			_ = sink.Send(ChatEvent{Type: EventDone})
			return sink.Flush()
		}
	}

	full, err := s.provider.StreamChat(streamCtx, req, func(delta string) error {
		if e := sink.Send(ChatEvent{Type: EventToken, Delta: delta}); e != nil {
			return e
		}
		return sink.Flush()
	})
	if err != nil {
		slog.Error("chat stream failed", "err", err, "conversation", conversationID)
		if full != "" {
			_, _ = s.msgs.Add(ctx, conversationID, model.MsgRoleAssistant, full)
		}
		return s.fail(sink, "the assistant could not complete the response")
	}

	if _, err := s.msgs.Add(ctx, conversationID, model.MsgRoleAssistant, full); err != nil {
		slog.Error("persist assistant message", "err", err)
	}

	if err := sink.Send(ChatEvent{Type: EventDone}); err != nil {
		return err
	}
	return sink.Flush()
}

func (s *Service) fail(sink EventSink, msg string) error {
	_ = sink.Send(ChatEvent{Type: EventError, Message: msg})
	_ = sink.Flush()
	return errors.New(msg)
}
