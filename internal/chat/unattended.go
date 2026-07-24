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
	"github.com/tamcore/kadence/internal/provider"
)

// UnattendedCatalog resolves immutable, owner-scoped tool snapshots suitable
// for both chat and unattended workers. Interactive-only built-ins are never
// included.
type UnattendedCatalog struct {
	mcp       MCPTools
	fitRoutes []FITRoute
}

type resolvedFITRoute struct {
	source       string
	downloadTool string
	analyzer     *fitactivity.Analyzer
}

// NewUnattendedCatalog constructs an owner-scoped tool catalog.
func NewUnattendedCatalog(mcp MCPTools, fitRoutes []FITRoute) *UnattendedCatalog {
	return &UnattendedCatalog{mcp: mcp, fitRoutes: append([]FITRoute(nil), fitRoutes...)}
}

// UnattendedSnapshot is one user's immutable tool list and dispatch route.
type UnattendedSnapshot struct {
	mcp       MCPUserSnapshot
	tools     []provider.ToolDefinition
	allowed   map[string]struct{}
	fitRoutes []resolvedFITRoute
}

// SnapshotFor resolves username once, eagerly lists its tools, and freezes the
// exact definitions and routes used for all later calls.
func (c *UnattendedCatalog) SnapshotFor(ctx context.Context, username string) (*UnattendedSnapshot, error) {
	snapshot := &UnattendedSnapshot{allowed: make(map[string]struct{})}
	if c == nil || c.mcp == nil || !c.mcp.Enabled() {
		return snapshot, nil
	}
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
		if definition.Name == credsToolName || definition.Name == loadSkillToolName {
			continue
		}
		if len(snapshot.fitRoutes) > 0 && definition.Name == analyzeGarminFITToolName {
			continue
		}
		if _, exists := snapshot.allowed[definition.Name]; exists {
			continue
		}
		snapshot.allowed[definition.Name] = struct{}{}
		snapshot.tools = append(snapshot.tools, definition)
	}
	if len(snapshot.fitRoutes) > 0 {
		snapshot.allowed[analyzeGarminFITToolName] = struct{}{}
		snapshot.tools = append(snapshot.tools, fitToolDefinition(snapshot.fitRoutes))
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
func (s *UnattendedSnapshot) Call(ctx context.Context, toolName, argsJSON string) (string, error) {
	if s == nil {
		return "", errors.New("chat: no unattended tool snapshot")
	}
	if _, ok := s.allowed[toolName]; !ok {
		return "", fmt.Errorf("chat: tool %q is not authorized in snapshot", toolName)
	}
	if toolName == analyzeGarminFITToolName {
		return s.callFIT(ctx, argsJSON)
	}
	if s.mcp == nil {
		return "", errors.New("chat: no MCP snapshot")
	}
	return s.mcp.Call(ctx, toolName, argsJSON)
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
	activity, err := analyzer.Analyze(ctx, s.mcp, args.ActivityID)
	if err != nil {
		slog.Warn("FIT analysis failed", "stage", fitactivity.FailureStage(err))
		return "", errors.New("could not analyze FIT activity")
	}
	data, err := json.Marshal(activity)
	if err != nil {
		return "", errors.New("could not encode activity analysis")
	}
	return string(data), nil
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
