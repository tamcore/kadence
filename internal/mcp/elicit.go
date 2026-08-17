package mcp

import (
	"context"
	"errors"
	"log/slog"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// confirmField is the single property upstream's confirmation schema asks for.
const confirmField = "confirm"

// JSON Schema keywords and type names the confirmation shape is checked against.
const (
	schemaKeyType       = "type"
	schemaKeyRequired   = "required"
	schemaKeyProperties = "properties"
	schemaTypeObject    = "object"
	schemaTypeBoolean   = "boolean"
)

// Reasons an elicitation is refused before anyone is asked.
var (
	errUnsupportedMode   = errors.New("mcp: only form-mode elicitation is supported")
	errUnsupportedSchema = errors.New("mcp: only a single required boolean confirmation is supported")
)

// ConfirmSource asks one user to confirm one tool call and blocks until they
// answer, decline, or the wait elapses. An error means nobody could be asked —
// a background turn with no live stream, say — which is a refusal, not a fault.
type ConfirmSource interface {
	Confirm(ctx context.Context, userID int64, tool, prompt string) (bool, error)
}

// SetConfirmSource installs the path from a mid-call question to a user.
// Without one, a server that insists on confirmation can never be satisfied,
// and its calls are refused rather than left hanging.
func (r *Registry) SetConfirmSource(src ConfirmSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.confirms = src
}

func (r *Registry) confirmSource() ConfirmSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.confirms
}

// confirmTarget names who is being asked and about what. It travels on the
// context of a single tool call rather than in a map on the registry: the MCP
// client is cached and shared, so any state hanging off it would let one
// turn's question surface in another turn — or in a scheduled run that has no
// user watching at all.
type confirmTarget struct {
	userID int64
	tool   string
	src    ConfirmSource
}

type confirmTargetKey struct{}

func withConfirmTarget(ctx context.Context, t confirmTarget) context.Context {
	return context.WithValue(ctx, confirmTargetKey{}, t)
}

func confirmTargetFrom(ctx context.Context) (confirmTarget, bool) {
	t, ok := ctx.Value(confirmTargetKey{}).(confirmTarget)
	return t, ok && t.src != nil
}

// elicitHandler answers a server's elicitation/create by putting the question
// to the user who caused it. It holds no state: everything it needs arrives on
// the context of the call being confirmed.
type elicitHandler struct{}

// Elicit implements client.ElicitationHandler.
//
// It never returns an error. An error would tell the server the client is
// broken; every failure here means the same thing to the server — the user did
// not confirm — which the protocol spells as a cancel.
func (elicitHandler) Elicit(ctx context.Context, req mcpgo.ElicitationRequest) (*mcpgo.ElicitationResult, error) {
	target, ok := confirmTargetFrom(ctx)
	if !ok {
		// Nothing on this context identifies a caller, so there is no one to
		// ask. This is the standalone listening stream, a dial-time question,
		// or a turn with no confirmation path configured.
		slog.Warn("mcp: refusing an elicitation with no caller to attribute it to")
		return cancelled(), nil
	}

	if err := validateConfirmRequest(req.Params); err != nil {
		// We answer exactly one question: a single boolean confirmation.
		// Anything else would have us collecting input we never showed the
		// user, so it is refused before anyone is asked.
		slog.Warn("mcp: refusing an unexpected elicitation",
			"tool", target.tool, "mode", req.Params.Mode, "err", err)
		return cancelled(), nil
	}

	allowed, err := target.src.Confirm(ctx, target.userID, target.tool, req.Params.Message)
	if err != nil {
		slog.Info("mcp: could not put a confirmation to the user",
			"tool", target.tool, "err", err)
		return cancelled(), nil
	}
	if !allowed {
		return &mcpgo.ElicitationResult{
			ElicitationResponse: mcpgo.ElicitationResponse{Action: mcpgo.ElicitationResponseActionDecline},
		}, nil
	}
	return &mcpgo.ElicitationResult{
		ElicitationResponse: mcpgo.ElicitationResponse{
			Action:  mcpgo.ElicitationResponseActionAccept,
			Content: map[string]any{confirmField: true},
		},
	}, nil
}

func cancelled() *mcpgo.ElicitationResult {
	return &mcpgo.ElicitationResult{
		ElicitationResponse: mcpgo.ElicitationResponse{Action: mcpgo.ElicitationResponseActionCancel},
	}
}

// validateConfirmRequest accepts only the shape this client can honestly
// answer: form mode asking for exactly one required boolean named confirm.
func validateConfirmRequest(p mcpgo.ElicitationParams) error {
	if p.Mode != "" && p.Mode != mcpgo.ElicitationModeForm {
		return errUnsupportedMode
	}
	schema, ok := p.RequestedSchema.(map[string]any)
	if !ok {
		return errUnsupportedSchema
	}
	if kind, _ := schema[schemaKeyType].(string); kind != schemaTypeObject {
		return errUnsupportedSchema
	}
	required, ok := schema[schemaKeyRequired].([]any)
	if !ok || len(required) != 1 {
		return errUnsupportedSchema
	}
	if name, _ := required[0].(string); name != confirmField {
		return errUnsupportedSchema
	}
	props, ok := schema[schemaKeyProperties].(map[string]any)
	if !ok || len(props) != 1 {
		return errUnsupportedSchema
	}
	field, ok := props[confirmField].(map[string]any)
	if !ok {
		return errUnsupportedSchema
	}
	if kind, _ := field[schemaKeyType].(string); kind != schemaTypeBoolean {
		return errUnsupportedSchema
	}
	return nil
}
