package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tamcore/kadence/internal/chat/skill"
	"github.com/tamcore/kadence/internal/mcpaudit"
	"github.com/tamcore/kadence/internal/mcpintent"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/secret"
)

// tools (capped, reserving one slot per enabled built-in — load_skill and/or
// request_credentials) plus the built-in tools themselves.
func (s *Service) assembleTools(ctx context.Context, mcpSnap MCPUserSnapshot) []provider.ToolDefinition {
	var tools []provider.ToolDefinition
	var guardedFITTool *provider.ToolDefinition
	fitRoutes := resolveFITRoutes(mcpSnap, s.fitRoutes)
	fitEnabled := len(fitRoutes) > 0
	if mcpSnap != nil {
		mcpTools, toolsErr := mcpSnap.ToolsFor(ctx)
		if toolsErr != nil {
			slog.Warn("mcp tools list failed, proceeding", "err", toolsErr)
		} else {
			filtered := mcpTools[:0]
			for _, definition := range mcpTools {
				if definition.Name == convertPaceToolName {
					continue
				}
				if fitEnabled && definition.Name == analyzeGarminFITToolName {
					captured := definition
					guardedFITTool = &captured
					continue
				}
				filtered = append(filtered, definition)
			}
			mcpTools = filtered
			// Reserve one slot per enabled built-in tool so the total never
			// exceeds the configured cap.
			mcpCap := s.maxTools
			builtins := 1
			if s.skills != nil {
				builtins++
			}
			if s.secrets != nil {
				builtins++
			}
			if fitEnabled {
				builtins++
			}
			if s.scheduled != nil {
				builtins++
			}
			mcpCap = max(mcpCap-builtins, 0)
			if len(mcpTools) > mcpCap {
				slog.Warn("mcp tools capped", "have", len(mcpTools), "cap", mcpCap)
				mcpTools = mcpTools[:mcpCap]
			}
			tools = mcpTools
		}
	}
	tools = append(tools, paceToolDefinition())
	if s.scheduled != nil {
		tools = append(tools, draftFutureUnattendedTaskToolDefinition())
	}
	if s.skills != nil {
		tools = append(tools, s.skillTool())
	}
	if s.secrets != nil {
		tools = append(tools, s.credsTool())
	}
	if fitEnabled {
		if guardedFITTool != nil {
			tools = append(tools, *guardedFITTool)
		} else {
			tools = append(tools, fitToolDefinition(fitRoutes))
		}
	}
	return tools
}

func fitToolDefinition(routes []resolvedFITRoute) provider.ToolDefinition {
	definition := provider.ToolDefinition{
		Name:        analyzeGarminFITToolName,
		Description: "Download and analyze one activity FIT file by activity_id. Returns a compact metric summary and splits, never GPS records.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"activity_id":{"type":"integer"}},"required":["activity_id"]}`),
	}
	if len(routes) <= 1 {
		return definition
	}

	sources := make([]string, 0, len(routes))
	for _, route := range routes {
		sources = append(sources, route.source)
	}
	schema := map[string]any{
		jsonSchemaType: "object",
		"properties": map[string]any{
			"activity_id": map[string]any{jsonSchemaType: "integer"},
			"source": map[string]any{
				jsonSchemaType: "string",
				"enum":         sources,
				"description":  "FIT-capable MCP source to use",
			},
		},
		"required": []string{"activity_id", "source"},
	}
	if data, err := json.Marshal(schema); err == nil {
		definition.Parameters = data
		definition.Description += " Select the source when more than one FIT-capable MCP is available."
	}
	return definition
}

// credsTool builds the built-in request_credentials tool definition.
func (s *Service) credsTool() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name: credsToolName,
		Description: "Ask the user to securely provide credentials (e.g. a login password or API key) that a " +
			"tool needs. The user is prompted through a secure form; you never see the raw values, only opaque " +
			"placeholder tokens to pass to the tool that needs them.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"reason": {"type": "string", "description": "why the credentials are needed"},
				"fields": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"label": {"type": "string"},
							"secret": {"type": "boolean"}
						},
						"required": ["name"]
					}
				}
			},
			"required": ["reason", "fields"]
		}`),
	}
}

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

func (s *Service) dispatchToolWithTurn(
	ctx, streamCtx context.Context, conversationID string, userID int64, uc UserContext,
	sourceUser model.Message, history []model.Message, mcpSnap MCPUserSnapshot, tc provider.ToolCall,
	gated map[string]bool, state *toolTurnState, redactor *turnRedactor, sink EventSink,
) provider.Message {
	toolCtx := mcpaudit.WithMetadata(streamCtx, mcpaudit.Metadata{
		ActorUserID: userID, ActorUsername: uc.Username, ConversationID: conversationID,
		Source: model.MCPAuditSourceChat, Model: s.cfg.Model, ToolCallID: tc.ID,
		RequestedTool: tc.Name, SafeArguments: safeMCPArguments(tc.Arguments),
		Sanitize: func(value string) string {
			if s.secrets == nil {
				return value
			}
			return secret.Redact(value, redactor.snapshot(s.secrets, userID))
		},
	})
	toolCtx = mcpaudit.WithPersistenceContext(toolCtx, ctx)
	if tc.Name == legacyDraftScheduledTaskToolName {
		return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "error: legacy scheduled handoff tool is unavailable"}
	}
	if s.scheduled != nil && tc.Name == draftFutureUnattendedTaskToolName {
		return s.handleDraftScheduledTask(toolCtx, conversationID, scheduled.Actor{
			ID: userID, Username: uc.Username, Timezone: uc.Timezone,
		}, sourceUser.Content, sourceUser.ID, history, state, tc, sink)
	}
	if s.secrets != nil && tc.Name == credsToolName {
		return s.handleRequestCredentials(toolCtx, userID, tc, sink)
	}
	if s.skills != nil {
		if tc.Name == loadSkillToolName {
			return s.handleLoadSkill(tc, gated, sink)
		}
		if sk, ok := s.skills.ForTool(tc.Name); ok && !gated[sk.Name] {
			gated[sk.Name] = true
			return s.gateWithSkill(tc, sk, sink)
		}
	}
	if tc.Name == convertPaceToolName {
		return s.handlePaceConversion(tc, sink)
	}
	if tc.Name == analyzeGarminFITToolName {
		return s.handleFITAnalysis(toolCtx, mcpSnap, tc, sink)
	}
	return s.runToolCall(toolCtx, userID, mcpSnap, tc, redactor, sink)
}

func (s *Service) handleFITAnalysis(ctx context.Context, mcpSnap MCPUserSnapshot, tc provider.ToolCall, sink EventSink) provider.Message {
	safeArguments := safeMCPArguments(tc.Arguments)
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: safeArguments})
	_ = sink.Flush()
	var out string
	var err error
	if mcpSnap == nil {
		err = errors.New("no FIT source is available")
	} else {
		out, err = mcpSnap.CallWithTransform(ctx, tc.Name, tc.Arguments, IdentityArguments)
	}
	status := toolStatusDone
	if err != nil {
		status = toolStatusError
		if blocked, ok := mcpintent.AsBlocked(err); ok {
			out = "error: " + blocked.Error()
		} else {
			out = fitAnalysisErrorMessage
		}
	}
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()
	return fencedToolResultMessage(tc, out)
}

// credentialRequestArgs is the parsed request_credentials tool-call payload.
type credentialRequestArgs struct {
	Reason string            `json:"reason"`
	Fields []CredentialField `json:"fields"`
}

// handleRequestCredentials intercepts the built-in request_credentials tool:
// it parses the requested fields, registers a broker request, emits a
// credentials_request SSE event (field specs + requestId + reason only —
// never a value or token), then blocks on broker.Await until the user
// submits (via the secure submit endpoint), the request times out, or
// streamCtx is cancelled (e.g. client disconnect). On success the tool
// result carries the field-name -> TOKEN map (never raw values) plus an
// instruction for the model; on timeout/cancel it carries a benign
// "not completed" status only.
func (s *Service) handleRequestCredentials(streamCtx context.Context, userID int64, tc provider.ToolCall, sink EventSink) provider.Message {
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: safeMCPArguments(tc.Arguments)})
	_ = sink.Flush()

	var args credentialRequestArgs
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || len(args.Fields) == 0 || len(args.Fields) > maxCredentialFields {
		_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusError})
		_ = sink.Flush()
		return provider.Message{
			Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "invalid credential request: fields must be a non-empty array of at most " +
				strconv.Itoa(maxCredentialFields) + " entries",
		}
	}

	fields := make([]secret.Field, len(args.Fields))
	for i, f := range args.Fields {
		fields[i] = secret.Field{Name: f.Name, Label: f.Label, Secret: f.Secret}
	}

	reqID, tokens, err := s.secrets.NewRequest(userID, fields)
	if err != nil {
		_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusError})
		_ = sink.Flush()
		return provider.Message{
			Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "invalid credential request: " + err.Error(),
		}
	}

	_ = sink.Send(ChatEvent{
		Type: EventCredentials, RequestID: reqID, Reason: args.Reason, Fields: args.Fields,
	})
	_ = sink.Flush()

	awaitErr := s.secrets.Await(streamCtx, reqID)

	status := toolStatusDone
	var content string
	if awaitErr != nil {
		status = toolStatusError
		content = credentialsNotCompletedResult
	} else {
		tokensJSON, mErr := json.Marshal(tokens)
		if mErr != nil {
			status = toolStatusError
			content = credentialsNotCompletedResult
		} else {
			content = string(tokensJSON) + "\n\n" + credentialsInstructionSuffix
		}
	}

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()

	return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: content}
}

// handleLoadSkill answers a load_skill call with the requested skill body.
func (s *Service) handleLoadSkill(
	tc provider.ToolCall, gated map[string]bool, sink EventSink,
) provider.Message {
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: safeMCPArguments(tc.Arguments)})
	_ = sink.Flush()

	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(tc.Arguments), &args)

	skillList := s.skills.List()
	content, status := "", toolStatusDone
	if sk, ok := s.skills.Get(args.Name); ok {
		content = sk.Body
		gated[sk.Name] = true
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
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: safeMCPArguments(tc.Arguments)})
	_ = sink.Flush()

	content := sk.Body +
		"\n\nBefore this call runs: review the guidance above, then re-issue the tool call so it complies (or confirm it already does)."

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusDone})
	_ = sink.Flush()
	return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: content}
}

// runToolCall dispatches a single tool call through mcpSnap (this turn's
// resolved MCP servers), emitting running/done/error tool events on sink,
// and returns the resulting role:"tool" message to append to the provider
// request.
//
// Order is security-critical (see docs/superpowers/specs — "Substitution at
// dispatch"): only the authorized clean arguments reach broker.Substitute.
// The MCP result is redacted before it is logged, streamed, or appended to
// the provider request.
func (s *Service) runToolCall(
	ctx context.Context, userID int64, mcpSnap MCPUserSnapshot, tc provider.ToolCall, redactor *turnRedactor, sink EventSink,
) provider.Message {
	safeArguments := safeMCPArguments(tc.Arguments)
	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: safeArguments})
	_ = sink.Flush()

	// Redaction values are snapshotted into redactor BEFORE Substitute runs:
	// Substitute consumes (deletes) each token's stored value as it
	// substitutes it (single-use), so a live value used in THIS call would
	// otherwise be gone from ActiveValues by the time we redact the result
	// below (and for the rest of the turn), even though it's still exactly
	// the value that could leak back in the tool's output or later text.
	var redactValues []string
	transform := IdentityArguments
	if s.secrets != nil {
		redactValues = redactor.snapshot(s.secrets, userID)
		transform = func(arguments string) (string, error) {
			transformed, _ := s.secrets.Substitute(userID, arguments)
			return transformed, nil
		}
	}

	var out string
	var cErr error
	if mcpSnap != nil {
		// A tool may ask this user to confirm mid-call. The sink rides the
		// call's own context so the question can only ever surface in the turn
		// that raised it.
		out, cErr = mcpSnap.CallWithTransform(WithConfirmSink(ctx, sink), tc.Name, tc.Arguments, transform)
	} else {
		cErr = fmt.Errorf("mcp: no MCP servers available for tool %q", tc.Name)
	}
	status := toolStatusDone
	if cErr != nil {
		if blocked, ok := mcpintent.AsBlocked(cErr); ok {
			out = "error: " + blocked.Error()
		} else {
			errText := cErr.Error()
			if s.secrets != nil {
				errText = secret.Redact(errText, redactValues)
			}
			slog.Warn("mcp tool call failed", "tool", tc.Name, "err", errText)
			out = "error: " + errText
		}
		status = toolStatusError
	} else if s.secrets != nil {
		out = secret.Redact(out, redactValues)
	}

	if cErr == nil {
		// Debug-only: surfaces exactly what a tool returned (enable via
		// KADENCE_LOG_LEVEL=debug) to diagnose "tool returned X but the model
		// said Y" cases. Result is truncated to keep logs bounded. Logs clean
		// model arguments and the already-redacted result.
		slog.Debug("mcp tool call", "tool", tc.Name, "args", safeArguments,
			"result_bytes", len(out), "result_preview", preview(out, 500))
	}

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()

	return fencedToolResultMessage(tc, out)
}
