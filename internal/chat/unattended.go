package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	fitactivity "github.com/tamcore/kadence/internal/fit"
	"github.com/tamcore/kadence/internal/mcpaudit"
	"github.com/tamcore/kadence/internal/mcpintent"
	"github.com/tamcore/kadence/internal/provider"
)

// UnattendedCatalog resolves immutable, owner-scoped tool snapshots suitable
// for both chat and unattended workers. Interactive-only built-ins are never
// included; shared local tools are.
type UnattendedCatalog struct {
	mcp         MCPTools
	fitRoutes   []FITRoute
	audit       *mcpaudit.Recorder
	intentGuard mcpintent.Evaluator
	log         *slog.Logger
}

type resolvedFITRoute struct {
	source       string
	downloadTool string
	analyzer     *fitactivity.Analyzer
}

// NewUnattendedCatalog constructs an owner-scoped tool catalog.
func NewUnattendedCatalog(mcp MCPTools, fitRoutes []FITRoute, audit *mcpaudit.Recorder, intentGuard mcpintent.Evaluator) *UnattendedCatalog {
	return &UnattendedCatalog{
		mcp:         mcp,
		fitRoutes:   append([]FITRoute(nil), fitRoutes...),
		audit:       audit,
		intentGuard: intentGuard,
		log:         slog.Default(),
	}
}

// UnattendedSnapshot is one user's immutable tool list and dispatch route.
type UnattendedSnapshot struct {
	mcp         MCPUserSnapshot
	tools       []provider.ToolDefinition
	allowed     map[string]struct{}
	fitRoutes   []resolvedFITRoute
	audit       *mcpaudit.Recorder
	definitions map[string]provider.ToolDefinition
	intentGuard mcpintent.Evaluator
}

// SnapshotFor resolves username once, eagerly lists its tools, and freezes the
// exact definitions and routes used for all later calls.
func (c *UnattendedCatalog) SnapshotFor(ctx context.Context, username string) (*UnattendedSnapshot, error) {
	var intentGuard mcpintent.Evaluator
	if c != nil {
		intentGuard = c.intentGuard
	}
	snapshot := &UnattendedSnapshot{
		tools:       []provider.ToolDefinition{paceToolDefinition()},
		allowed:     map[string]struct{}{convertPaceToolName: {}},
		definitions: map[string]provider.ToolDefinition{convertPaceToolName: paceToolDefinition()},
		intentGuard: intentGuard,
	}
	if c == nil || c.mcp == nil || !c.mcp.Enabled() {
		return snapshot, nil
	}
	snapshot.audit = c.audit
	snapshot.mcp = c.mcp.SnapshotFor(ctx, username)
	if snapshot.mcp == nil {
		return snapshot, nil
	}
	definitions, err := snapshot.mcp.ToolsFor(ctx)
	if err != nil {
		return nil, err
	}
	snapshot.fitRoutes = resolveFITRoutes(snapshot.mcp, c.fitRoutes)
	for _, definition := range definitions {
		if definition.Name == credsToolName ||
			definition.Name == loadSkillToolName ||
			definition.Name == convertPaceToolName {
			continue
		}
		if len(snapshot.fitRoutes) > 0 && definition.Name == analyzeGarminFITToolName {
			continue
		}
		if _, exists := snapshot.allowed[definition.Name]; exists {
			continue
		}
		if snapshot.intentGuard != nil {
			guarded, guardErr := mcpintent.AugmentTool(definition)
			if guardErr != nil {
				logger := c.log
				if logger == nil {
					logger = slog.Default()
				}
				category := "invalid"
				var schemaErr *mcpintent.SchemaError
				if errors.As(guardErr, &schemaErr) {
					category = schemaErr.Category
				}
				logger.Warn("MCP tool omitted from intent guard catalog", "tool", definition.Name, "category", category)
				continue
			}
			definition = guarded
		}
		snapshot.allowed[definition.Name] = struct{}{}
		snapshot.tools = append(snapshot.tools, definition)
		snapshot.definitions[definition.Name] = definition
	}
	if len(snapshot.fitRoutes) > 0 {
		definition := fitToolDefinition(snapshot.fitRoutes)
		if snapshot.intentGuard != nil {
			guarded, guardErr := mcpintent.AugmentTool(definition)
			if guardErr == nil {
				definition = guarded
			} else {
				logger := c.log
				if logger == nil {
					logger = slog.Default()
				}
				logger.Warn("MCP tool omitted from intent guard catalog", "tool", definition.Name, "category", "invalid")
				return snapshot, nil
			}
		}
		snapshot.allowed[analyzeGarminFITToolName] = struct{}{}
		snapshot.tools = append(snapshot.tools, definition)
		snapshot.definitions[definition.Name] = definition
	}
	return snapshot, nil
}

// ToolsFor returns a copy of the exact definitions frozen in this snapshot.
func (s *UnattendedSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	if s == nil {
		return nil, nil
	}
	return append([]provider.ToolDefinition(nil), s.tools...), nil
}

// Call dispatches only a name present in the frozen tool list.
type ArgumentTransform func(string) (string, error)

func IdentityArguments(arguments string) (string, error) { return arguments, nil }

func (s *UnattendedSnapshot) Call(ctx context.Context, toolName, argsJSON string) (string, error) {
	return s.CallWithTransform(ctx, toolName, argsJSON, IdentityArguments)
}

func (s *UnattendedSnapshot) CallWithTransform(ctx context.Context, toolName, argsJSON string, transform ArgumentTransform) (string, error) {
	if s == nil {
		return "", errors.New("chat: no unattended tool snapshot")
	}
	if _, ok := s.allowed[toolName]; !ok {
		return "", fmt.Errorf("chat: tool %q is not authorized in snapshot", toolName)
	}
	if toolName == convertPaceToolName {
		return callPaceTool(argsJSON)
	}
	if transform == nil {
		transform = IdentityArguments
	}
	if s.intentGuard != nil {
		return s.callGuardedDirect(ctx, toolName, argsJSON, transform)
	}
	if toolName == analyzeGarminFITToolName {
		arguments, err := transform(argsJSON)
		if err != nil {
			return "", err
		}
		return s.callFIT(ctx, arguments)
	}
	if s.mcp == nil {
		return "", errors.New("chat: no MCP snapshot")
	}
	arguments, err := transform(argsJSON)
	if err != nil {
		return "", err
	}
	return s.callAllowedRemote(ctx, toolName, arguments)
}

// ToolHints retains the MCPUserSnapshot contract for interactive chat.
func (s *UnattendedSnapshot) ToolHints() []string {
	if s == nil || s.mcp == nil {
		return nil
	}
	return s.mcp.ToolHints()
}

// ServerPrefix retains the route visibility contract for tests and callers.
func (s *UnattendedSnapshot) ServerPrefix(name, scope string) (string, bool) {
	resolver, ok := s.mcp.(mcpServerPrefixResolver)
	if !ok {
		return "", false
	}
	return resolver.ServerPrefix(name, scope)
}

func (s *UnattendedSnapshot) callGuardedDirect(ctx context.Context, toolName, arguments string, transform ArgumentTransform) (string, error) {
	parsed, err := mcpintent.ExtractArguments(arguments)
	if err != nil {
		return "", s.block(ctx, toolName, mcpintent.StripArguments(arguments), "", mcpintent.Decision{Verdict: mcpintent.VerdictDeny, Reason: "tool intent is invalid"})
	}
	ctx, err = s.authorize(ctx, toolName, parsed.SafeJSON, parsed.Intent)
	if err != nil {
		return "", err
	}
	clean, err := transform(parsed.SafeJSON)
	if err != nil {
		return "", err
	}
	ctx = mcpintent.WithInheritedIntent(ctx, parsed.Intent)
	if toolName == analyzeGarminFITToolName {
		return s.callFIT(ctx, clean)
	}
	return s.callAllowedRemote(ctx, toolName, clean)
}

func (s *UnattendedSnapshot) callFIT(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ActivityID int64  `json:"activity_id"`
		Source     string `json:"source"`
	}
	decoder := json.NewDecoder(strings.NewReader(argsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || args.ActivityID <= 0 {
		return "", errors.New("activity_id must be a positive integer")
	}
	var analyzer *fitactivity.Analyzer
	switch {
	case args.Source != "":
		for _, route := range s.fitRoutes {
			if route.source == args.Source {
				analyzer = route.analyzer
				break
			}
		}
	case len(s.fitRoutes) == 1:
		analyzer = s.fitRoutes[0].analyzer
	}
	if analyzer == nil {
		return "", errors.New("no FIT source is available")
	}
	activity, err := analyzer.Analyze(ctx, remoteCaller{snapshot: s}, args.ActivityID)
	if err != nil {
		if _, blocked := mcpintent.AsBlocked(err); blocked {
			return "", err
		}
		slog.Warn("FIT analysis failed", "stage", fitactivity.FailureStage(err))
		return "", errors.New("could not analyze FIT activity")
	}
	data, err := json.Marshal(activity)
	if err != nil {
		return "", errors.New("could not encode activity analysis")
	}
	return string(data), nil
}

type remoteCaller struct{ snapshot *UnattendedSnapshot }

func (c remoteCaller) Call(ctx context.Context, toolName, arguments string) (string, error) {
	return c.snapshot.callRemote(ctx, toolName, arguments)
}

func (s *UnattendedSnapshot) callRemote(ctx context.Context, toolName, arguments string) (string, error) {
	if s.intentGuard != nil {
		intent, ok := mcpintent.InheritedIntentFrom(ctx)
		if !ok {
			return "", s.block(ctx, toolName, arguments, "", mcpintent.Decision{Verdict: mcpintent.VerdictDeny, Reason: "tool intent is invalid"})
		}
		var err error
		ctx, err = s.authorize(ctx, toolName, arguments, intent)
		if err != nil {
			return "", err
		}
	}
	return s.callAllowedRemote(ctx, toolName, arguments)
}

func (s *UnattendedSnapshot) callAllowedRemote(ctx context.Context, toolName, arguments string) (string, error) {
	return s.audit.Call(ctx, toolName, arguments, func(callCtx context.Context) (string, error) {
		return s.mcp.Call(callCtx, toolName, arguments)
	})
}

func (s *UnattendedSnapshot) authorize(ctx context.Context, toolName, arguments, intent string) (context.Context, error) {
	definition, ok := s.definitions[toolName]
	if !ok {
		return ctx, s.block(ctx, toolName, arguments, intent, mcpintent.Decision{Verdict: mcpintent.VerdictDeny, Reason: "tool definition is unavailable"})
	}
	decision, err := s.intentGuard.Evaluate(ctx, mcpintent.Input{
		Intent: intent, ToolName: toolName, ToolDescription: definition.Description, Arguments: arguments,
	})
	if err != nil || decision.Verdict != mcpintent.VerdictAllow {
		if decision.Reason == "" {
			decision.Reason = "tool intent could not be approved"
		}
		return ctx, s.block(ctx, toolName, arguments, intent, decision)
	}
	metadata, hasMetadata := mcpaudit.MetadataFromContext(ctx)
	if hasMetadata {
		metadata.SafeArguments = arguments
		metadata.Intent = intent
		metadata.GuardVerdict = decision.Verdict
		metadata.GuardReason = decision.Reason
		ctx = mcpaudit.WithMetadata(ctx, metadata)
	}
	return ctx, nil
}

func (s *UnattendedSnapshot) block(ctx context.Context, toolName, arguments, intent string, decision mcpintent.Decision) error {
	if decision.Verdict != mcpintent.VerdictDeny {
		decision.Verdict = mcpintent.VerdictDeny
	}
	if decision.Reason == "" {
		decision.Reason = "tool intent could not be approved"
	}
	if metadata, ok := mcpaudit.MetadataFromContext(ctx); ok {
		metadata.SafeArguments = arguments
		metadata.Intent = intent
		metadata.GuardVerdict = decision.Verdict
		metadata.GuardReason = decision.Reason
		ctx = mcpaudit.WithMetadata(ctx, metadata)
	}
	s.audit.Block(ctx, toolName, arguments, intent, decision.Verdict, decision.Reason)
	return &mcpintent.BlockedError{Verdict: decision.Verdict, Reason: decision.Reason}
}

func resolveFITRoutes(mcpSnap MCPUserSnapshot, configured []FITRoute) []resolvedFITRoute {
	resolver, ok := mcpSnap.(mcpServerPrefixResolver)
	if !ok {
		return nil
	}
	routes := make([]resolvedFITRoute, 0, len(configured))
	for _, route := range configured {
		prefix, visible := resolver.ServerPrefix(route.ServerName, route.ServerScope)
		if !visible {
			continue
		}
		downloadTool := prefix + "__" + route.DownloadTool
		routes = append(routes, resolvedFITRoute{
			source:       prefix,
			downloadTool: downloadTool,
			analyzer: fitactivity.NewAnalyzer(
				downloadTool, route.BridgeURL, route.BridgeAuthUser,
				route.BridgeAuthPass, route.MaxBytes,
			),
		})
	}
	return routes
}
