package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"

	"github.com/tamcore/kadence/internal/provider"
)

// Registry holds the configured remote MCP servers and lazily-created
// clients for them, keyed by (Name, Scope). It exposes a per-user tool
// list (namespaced by server) and dispatches tool calls back to the
// owning server.
type Registry struct {
	servers []Server

	mu      sync.Mutex
	clients map[string]mcpClient // keyed by Name+"/"+Scope
}

// NewRegistry builds a Registry over the given servers. Clients are created
// lazily on first use.
func NewRegistry(servers []Server) *Registry {
	return &Registry{
		servers: servers,
		clients: make(map[string]mcpClient),
	}
}

// Enabled reports whether any MCP servers are configured.
func (r *Registry) Enabled() bool {
	return len(r.servers) > 0
}

// ToolsFor returns the tool definitions available to the given user: the
// union of all servers applicable to that user (GLOBAL ones plus their
// USER_<username> ones), with each tool namespaced as
// "<lowercased server name>__<tool>". A server that fails to connect or
// list its tools is logged and skipped (fail-soft) rather than failing the
// whole call.
func (r *Registry) ToolsFor(ctx context.Context, username string) ([]provider.ToolDefinition, error) {
	var defs []provider.ToolDefinition

	for _, s := range r.servers {
		if !s.AppliesTo(username) {
			continue
		}

		client, err := r.clientFor(ctx, s)
		if err != nil {
			slog.Warn("mcp: skipping server (connect failed)", "server", s.Name, "scope", s.Scope, "error", err)
			continue
		}

		tools, err := client.ListTools(ctx)
		if err != nil {
			slog.Warn("mcp: skipping server (list tools failed)", "server", s.Name, "scope", s.Scope, "error", err)
			continue
		}

		prefix := strings.ToLower(s.Name) + "__"
		for _, t := range tools {
			if !s.allowsTool(t.Name) {
				continue
			}
			defs = append(defs, provider.ToolDefinition{
				Name:        prefix + t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			})
		}
	}

	return defs, nil
}

// Call routes a namespaced tool call ("<server>__<tool>") to the owning
// server (applicable to username) and invokes it with the given
// JSON-encoded arguments.
func (r *Registry) Call(ctx context.Context, username, toolName, argsJSON string) (string, error) {
	serverName, realTool, ok := strings.Cut(toolName, "__")
	if !ok {
		return "", fmt.Errorf("mcp: invalid tool name %q (expected <server>__<tool>)", toolName)
	}

	s, ok := r.findApplicableServer(username, serverName)
	if !ok {
		return "", fmt.Errorf("mcp: no server %q available for user %q", serverName, username)
	}

	if !s.allowsTool(realTool) {
		return "", fmt.Errorf("mcp: tool %q is not enabled for server %q", realTool, serverName)
	}

	client, err := r.clientFor(ctx, s)
	if err != nil {
		return "", fmt.Errorf("mcp: connect to server %s: %w", s.Name, err)
	}

	return client.CallTool(ctx, realTool, argsJSON)
}

// allowsTool reports whether toolName (unprefixed) passes this server's TOOLS
// filter. No patterns configured → all tools allowed. A malformed pattern is
// skipped (never panics).
func (s Server) allowsTool(toolName string) bool {
	if len(s.Tools) == 0 {
		return true
	}
	for _, pat := range s.Tools {
		if ok, err := path.Match(pat, toolName); err == nil && ok {
			return true
		}
	}
	return false
}

// findApplicableServer finds the server matching serverName (case-insensitive
// on the server's Name) that applies to username.
func (r *Registry) findApplicableServer(username, serverName string) (Server, bool) {
	for _, s := range r.servers {
		if strings.EqualFold(s.Name, serverName) && s.AppliesTo(username) {
			return s, true
		}
	}
	return Server{}, false
}

// clientFor returns the cached client for the given server, creating and
// caching one if none exists yet.
func (r *Registry) clientFor(ctx context.Context, s Server) (mcpClient, error) {
	key := s.Name + "/" + s.Scope

	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok := r.clients[key]; ok {
		return c, nil
	}

	c, err := newClient(ctx, s)
	if err != nil {
		return nil, err
	}
	r.clients[key] = c
	return c, nil
}
