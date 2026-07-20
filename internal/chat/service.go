package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tamcore/kadence/internal/chat/skill"
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
	MCPMaxTools      int
	// Now supplies the current time used to stamp the system prompt with
	// today's date. Defaults to time.Now when nil (overridable in tests).
	Now func() time.Time
}

const defaultMaxToolIterations = 8
const defaultMaxTools = 100
const loadSkillToolName = "kadence__load_skill"

// toolMsgRole is the provider.Message.Role used for tool-result messages.
const toolMsgRole = "tool"

// Tool event statuses.
const (
	toolStatusRunning = "running"
	toolStatusDone    = "done"
	toolStatusError   = "error"
)

const defaultSystemPrompt = "You are Kadence, a knowledgeable and encouraging endurance-sports coach. " +
	"Give practical, safe, evidence-based training guidance. Be concise and supportive. " +
	"When tools are available, use them to answer questions about the user's data before responding. " +
	"Do not tell the user that something does not exist based on a single empty tool result — if a tool " +
	"returns nothing, consider whether a different, related tool would answer the question, and prefer " +
	"the broadest relevant tool. Only state that data is absent after genuinely checking.\n\n" +
	"Domain skills may be available to you: call the load_skill tool to load one when relevant, and when a " +
	"tool call returns skill guidance instead of running, follow it and re-issue the call correctly before proceeding."

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
	maxTools      int
	now           func() time.Time
	skills        *skill.Registry
}

// Deps carries the chat Service's dependencies. Guardrail, RAG, MCP, and
// Skills may be nil (disabled).
type Deps struct {
	Convs     ConversationStore
	Msgs      MessageStore
	Guardrail *Guardrail
	RAG       *RAG
	MCP       MCPTools
	// Skills, when non-nil, enables the skill subsystem (load_skill tool +
	// pre-gate injection).
	Skills *skill.Registry
}

// NewService constructs a chat Service. deps.Guardrail, deps.RAG, and deps.MCP
// may be nil (disabled).
func NewService(p provider.Provider, cfg ServiceConfig, deps Deps) *Service {
	maxIterations := cfg.MCPMaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxToolIterations
	}
	maxTools := cfg.MCPMaxTools
	if maxTools <= 0 {
		maxTools = defaultMaxTools
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		provider: p, cfg: cfg, convs: deps.Convs, msgs: deps.Msgs,
		guardrail: deps.Guardrail, rag: deps.RAG, mcp: deps.MCP,
		maxIterations: maxIterations, maxTools: maxTools, now: now,
		skills: deps.Skills,
	}
}

func (s *Service) systemPrompt() string {
	base := defaultSystemPrompt
	if s.cfg.SystemPrompt != "" {
		base = s.cfg.SystemPrompt
	}
	// Stamp the current date so the model resolves relative dates ("today",
	// "next week") and date-range tool arguments against the correct day
	// rather than its training cutoff.
	today := s.now()
	return base + "\n\nToday's date is " + today.Format("Monday, 2006-01-02") +
		". Use it to resolve relative dates and to choose date ranges when calling tools."
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
				if s.skills != nil {
					for _, sk := range s.skills.ForHistory() {
						req.Messages = insertAfterSystem(req.Messages,
							provider.Message{Role: model.MsgRoleSystem, Content: sk.Body})
					}
				}
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
			if len(tools) > s.maxTools {
				slog.Warn("mcp tools capped", "have", len(tools), "cap", s.maxTools)
				tools = tools[:s.maxTools]
			}
			req.Tools = tools
		}
	}

	if s.skills != nil {
		req.Tools = append(req.Tools, s.skillTool())
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

	gated := make(map[string]bool)
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
			req.Messages = append(req.Messages, s.dispatchTool(ctx, username, tc, gated, sink))
		}
	}

	return "", nil
}

// skillTool builds the built-in load_skill tool definition, listing available
// skills (names + one-line descriptions only) in its description.
func (s *Service) skillTool() provider.ToolDefinition {
	var b strings.Builder
	b.WriteString("Load the full guidance for a domain skill by name. ")
	b.WriteString("Call it when a listed skill is relevant to the user's request. Available skills:\n")
	for _, sk := range s.skills.List() {
		b.WriteString("- ")
		b.WriteString(sk.Name)
		b.WriteString(" — ")
		b.WriteString(sk.Description)
		b.WriteString("\n")
	}
	return provider.ToolDefinition{
		Name:        loadSkillToolName,
		Description: b.String(),
		Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"the skill name to load"}},"required":["name"]}`),
	}
}

// dispatchTool routes one tool call: the built-in load_skill and pre-gated
// triggering tools are handled locally; everything else goes to MCP. gated
// tracks which skills have already pre-gated a call this turn (so the retried
// call executes for real).
func (s *Service) dispatchTool(ctx context.Context, username string, tc provider.ToolCall, gated map[string]bool, sink EventSink) provider.Message {
	if s.skills != nil {
		if tc.Name == loadSkillToolName {
			return s.handleLoadSkill(tc, sink)
		}
		if sk, ok := s.skills.ForTool(tc.Name); ok && !gated[sk.Name] {
			gated[sk.Name] = true
			return s.gateWithSkill(tc, sk, sink)
		}
	}
	return s.runToolCall(ctx, username, tc, sink)
}

// handleLoadSkill answers a load_skill call with the requested skill body.
func (s *Service) handleLoadSkill(tc provider.ToolCall, sink EventSink) provider.Message {
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: tc.Arguments})
	_ = sink.Flush()

	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(tc.Arguments), &args)

	skillList := s.skills.List()
	content, status := "", toolStatusDone
	if sk, ok := s.skills.Get(args.Name); ok {
		content = sk.Body
	} else {
		status = toolStatusError
		names := make([]string, 0, len(skillList))
		for _, x := range skillList {
			names = append(names, x.Name)
		}
		content = "error: unknown skill " + args.Name + "; available: " + strings.Join(names, ", ")
	}

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()
	return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: content}
}

// gateWithSkill returns the skill body in place of executing the tool, prompting
// the model to review and re-issue the call.
func (s *Service) gateWithSkill(tc provider.ToolCall, sk skill.Skill, sink EventSink) provider.Message {
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: tc.Arguments})
	_ = sink.Flush()

	content := sk.Body +
		"\n\nBefore this call runs: review the guidance above, then re-issue the tool call so it complies (or confirm it already does)."

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusDone})
	_ = sink.Flush()
	return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: content}
}

// runToolCall dispatches a single tool call through s.mcp, emitting
// running/done/error tool events on sink, and returns the resulting
// role:"tool" message to append to the provider request.
func (s *Service) runToolCall(ctx context.Context, username string, tc provider.ToolCall, sink EventSink) provider.Message {
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: tc.Arguments})
	_ = sink.Flush()

	out, cErr := s.mcp.Call(ctx, username, tc.Name, tc.Arguments)
	status := toolStatusDone
	if cErr != nil {
		slog.Warn("mcp tool call failed", "tool", tc.Name, "err", cErr)
		out = "error: " + cErr.Error()
		status = toolStatusError
	}

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()

	return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: out}
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
