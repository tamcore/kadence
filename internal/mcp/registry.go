package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/tamcore/kadence/internal/provider"
)

// UserServerSource supplies per-user, DB-backed MCP servers (credentials
// decrypted) to merge with the env-configured ones. Implemented by the store.
type UserServerSource interface {
	ServersForUser(ctx context.Context, username string) ([]Server, error)
	AllServers(ctx context.Context) ([]Server, error)
}

// Registry holds the configured remote MCP servers and lazily-created
// clients for them, keyed by (Name, Scope). It exposes a per-user tool
// list (namespaced by server) and dispatches tool calls back to the
// owning server.
type Registry struct {
	servers    []Server
	httpClient *http.Client // optional CA-verifying client; nil = mcp-go default
	userSrc    UserServerSource

	mu      sync.Mutex
	clients map[string]mcpClient // keyed by Name+"/"+Scope; env servers only
}

// NewRegistry builds a Registry over the given servers. Clients are created
// lazily on first use. httpClient, if non-nil (e.g. from HTTPClientWithCA),
// is used for every server's transport instead of mcp-go's default client —
// used to verify MCP servers' TLS certs against a custom CA. Pass nil to
// preserve today's behavior (plaintext http, or https verified against the
// system trust store). userSrc, if non-nil, supplies per-user DB-backed MCP
// servers to merge with servers; pass nil to disable user-defined servers.
func NewRegistry(servers []Server, httpClient *http.Client, userSrc UserServerSource) *Registry {
	return &Registry{
		servers:    servers,
		httpClient: httpClient,
		userSrc:    userSrc,
		clients:    make(map[string]mcpClient),
	}
}

// Enabled reports whether any MCP servers are configured.
func (r *Registry) Enabled() bool {
	return len(r.servers) > 0 || r.userSrc != nil
}

// UserSnapshot is a per-turn resolved view of the servers applicable to one
// user, computed once via Registry.SnapshotFor. Reusing it through a chat
// turn's tool loop (instead of calling Registry.ToolsFor/Call repeatedly)
// avoids re-running the user-servers DB query (and re-decrypting their
// stored credentials) on every tool call within that turn.
type UserSnapshot struct {
	reg      *Registry
	username string
	servers  []Server
}

// SnapshotFor resolves the servers applicable to username once — env servers
// plus a single ServersForUser DB query — and returns a view reusable for
// the rest of a chat turn.
func (r *Registry) SnapshotFor(ctx context.Context, username string) *UserSnapshot {
	return &UserSnapshot{reg: r, username: username, servers: r.applicableServers(ctx, username)}
}

// ToolsFor returns the tool definitions available to this snapshot's user,
// using the servers resolved at snapshot time (no further DB queries).
func (u *UserSnapshot) ToolsFor(ctx context.Context) ([]provider.ToolDefinition, error) {
	return u.reg.toolsFor(ctx, u.username, u.servers)
}

// Call routes a namespaced tool call ("<server>__<tool>") using the servers
// resolved at snapshot time (no further DB queries).
func (u *UserSnapshot) Call(ctx context.Context, toolName, argsJSON string) (string, error) {
	return u.reg.call(ctx, u.username, u.servers, toolName, argsJSON)
}

// applicableServers returns env servers plus the user's own DB servers.
func (r *Registry) applicableServers(ctx context.Context, username string) []Server {
	out := append([]Server(nil), r.servers...)
	if r.userSrc != nil {
		us, err := r.userSrc.ServersForUser(ctx, username)
		if err != nil {
			slog.Warn("mcp: user server source failed", "user", username, "error", err)
		} else {
			out = append(out, us...)
		}
	}
	return out
}

// ToolsFor returns the tool definitions available to the given user: the
// union of all servers applicable to that user (GLOBAL ones plus their
// USER_<username> ones), with each tool namespaced as
// "<lowercased server name>__<tool>". A server that fails to connect or
// list its tools is logged and skipped (fail-soft) rather than failing the
// whole call.
func (r *Registry) ToolsFor(ctx context.Context, username string) ([]provider.ToolDefinition, error) {
	return r.toolsFor(ctx, username, r.applicableServers(ctx, username))
}

// toolsFor is the shared implementation behind ToolsFor and
// UserSnapshot.ToolsFor: it builds the tool-definition list from an
// already-resolved servers slice, without querying userSrc again.
func (r *Registry) toolsFor(ctx context.Context, username string, servers []Server) ([]provider.ToolDefinition, error) {
	var defs []provider.ToolDefinition

	for _, s := range servers {
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
			r.evictClient(s)
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
	return r.call(ctx, username, r.applicableServers(ctx, username), toolName, argsJSON)
}

// call is the shared implementation behind Call and UserSnapshot.Call: it
// dispatches against an already-resolved servers slice, without querying
// userSrc again.
func (r *Registry) call(ctx context.Context, username string, servers []Server, toolName, argsJSON string) (string, error) {
	serverName, realTool, ok := strings.Cut(toolName, "__")
	if !ok {
		return "", fmt.Errorf("mcp: invalid tool name %q (expected <server>__<tool>)", toolName)
	}

	s, ok := findApplicableServerIn(servers, username, serverName)
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

	out, err := client.CallTool(ctx, realTool, argsJSON)
	if err != nil {
		r.evictClient(s)
		return "", err
	}
	return out, nil
}

// Servers returns a copy of the configured env servers plus all users' DB
// servers (used by the health poller, which has no per-user context).
func (r *Registry) Servers() []Server {
	out := append([]Server(nil), r.servers...)
	if r.userSrc != nil {
		if all, err := r.userSrc.AllServers(context.Background()); err != nil {
			slog.Warn("mcp: all user servers failed", "error", err)
		} else {
			out = append(out, all...)
		}
	}
	return out
}

// Probe connects to s and lists its tools, returning only those allowed by
// the server's TOOLS filter (matching what ToolsFor/Call actually expose).
// Used by the health poller; reuses the cached client.
func (r *Registry) Probe(ctx context.Context, s Server) ([]ToolInfo, error) {
	client, err := r.clientFor(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("mcp: probe connect %s/%s: %w", s.Name, s.Scope, err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		r.evictClient(s)
		return nil, fmt.Errorf("mcp: probe list tools %s/%s: %w", s.Name, s.Scope, err)
	}
	allowed := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		if s.allowsTool(t.Name) {
			allowed = append(allowed, t)
		}
	}
	return allowed, nil
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

// findApplicableServerIn finds the server matching serverName
// (case-insensitive on the server's Name) within servers that applies to
// username, searching env servers before the user's own DB servers (the
// order applicableServers returns them in). On a name collision, the env
// server wins.
func findApplicableServerIn(servers []Server, username, serverName string) (Server, bool) {
	for _, s := range servers {
		if strings.EqualFold(s.Name, serverName) && s.AppliesTo(username) {
			return s, true
		}
	}
	return Server{}, false
}

// clientFor returns a client for the given server. Env-configured servers use
// a lazily-created, cached client (keyed by Name+Scope). User-defined (DB)
// servers always get a fresh client — they're not cached, since their
// credentials/URL can change or be revoked at any time.
func (r *Registry) clientFor(ctx context.Context, s Server) (mcpClient, error) {
	if !r.isEnvServer(s) {
		return newClient(ctx, s, r.httpClient)
	}

	key := s.Name + "/" + s.Scope

	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok := r.clients[key]; ok {
		return c, nil
	}

	c, err := newClient(ctx, s, r.httpClient)
	if err != nil {
		return nil, err
	}
	r.clients[key] = c
	return c, nil
}

// evictClient drops s's cached client (if any) so the next clientFor call
// redials instead of reusing a connection that just failed a probe or tool
// call. Only env-configured servers are ever cached (see clientFor); calling
// this for a user-defined server is a harmless no-op — those already get a
// fresh client on every call.
func (r *Registry) evictClient(s Server) {
	if !r.isEnvServer(s) {
		return
	}
	key := s.Name + "/" + s.Scope
	r.mu.Lock()
	delete(r.clients, key)
	r.mu.Unlock()
}

// isEnvServer reports whether s is one of the env-configured servers (vs. a
// per-user DB server supplied by userSrc).
func (r *Registry) isEnvServer(s Server) bool {
	for _, e := range r.servers {
		if e.Name == s.Name && e.Scope == s.Scope {
			return true
		}
	}
	return false
}
