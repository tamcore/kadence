package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tamcore/kadence/internal/provider"
)

// dialTimeout bounds a single shared dial (Start+Initialize handshake) kicked
// off by clientFor's singleflight group. It is deliberately its own constant
// rather than reusing defaultProbeTimeout (10s, health.go): a dial does more
// work than a probe (transport Start, then the Initialize round trip), and it
// runs detached from any caller's context (see clientFor), so it needs its
// own bound to guarantee it can't hang forever once nothing is left to cancel
// it. 15s gives that handshake headroom over a probe while still failing well
// within any reasonable request timeout.
const dialTimeout = 15 * time.Second

// dialClient is the seam onto newClient so tests can return fakes instead of
// dialing a real transport. Production always uses newClient.
var dialClient = newClient

// UserServerSource supplies per-user, DB-backed MCP servers (credentials
// decrypted) to merge with the env-configured ones. Implemented by the store.
type UserServerSource interface {
	ServersForUser(ctx context.Context, username string) ([]Server, error)
	AllServers(ctx context.Context) ([]Server, error)
}

// leasedClient tracks how many callers currently hold a cached client. A
// client evicted (or dropped by Registry.Close) while still in use is removed
// from the cache immediately — so the next dispatch re-dials — but its
// transport is closed only once the last holder has released it. Without
// this, evicting on a transient failure would close a transport another
// goroutine is mid-call on.
type leasedClient struct {
	client  mcpClient
	leases  int
	evicted bool
	closed  bool
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
	clients map[string]*leasedClient // keyed by Name+"/"+Scope; env servers only
	closed  bool                     // set by Close; stops a late dial re-caching

	// dial deduplicates concurrent first-time dials to the same env server:
	// callers racing on a cache miss for the same key share one in-flight
	// newClient call (and its result/error) instead of each dialing
	// independently. Different keys dial fully in parallel — dialing never
	// happens under mu, so a slow/hanging dial for one server can never block
	// map lookups (cache hits) or dials to other servers.
	dial singleflight.Group
}

// NewRegistry builds a Registry over the given servers. Clients are created
// lazily on first use. httpClient, if non-nil (e.g. from HTTPClientWithCA),
// is used for every server's transport instead of mcp-go's default client —
// used to verify MCP servers' TLS certs against a custom CA. Pass nil to
// preserve today's behavior (plaintext http, or https verified against the
// system trust store). userSrc, if non-nil, supplies per-user DB-backed MCP
// servers to merge with servers; pass nil to disable user-defined servers.
func NewRegistry(servers []Server, httpClient *http.Client, userSrc UserServerSource) *Registry {
	owned := append([]Server(nil), servers...)
	for i := range owned {
		owned[i].FromEnv = true
	}
	return &Registry{
		servers:    owned,
		httpClient: httpClient,
		userSrc:    userSrc,
		clients:    make(map[string]*leasedClient),
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

// ToolHints returns one "Tool guide: <prefix>: <hint>" line per server
// (resolved at snapshot time, applicable to this user) that has a non-empty
// Hint — in the same order those servers would be listed by ToolsFor. A
// server without a Hint contributes no line. This never touches the
// network: it's derived from data already resolved by SnapshotFor.
func (u *UserSnapshot) ToolHints() []string {
	return u.reg.toolHints(u.username, u.servers)
}

// ServerPrefix returns the effective tool-name prefix for one exact
// env-configured MCP server when that server is visible in this user's snapshot.
// It accounts for aliases and per-user collision fallback in the same way as
// ToolsFor and Call.
func (u *UserSnapshot) ServerPrefix(name, scope string) (string, bool) {
	applicable, prefixes := applicableServersWithPrefixes(u.username, u.servers)
	for i, server := range applicable {
		if server.Name == name && server.Scope == scope {
			return prefixes[i], true
		}
	}
	return "", false
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

	applicable, prefixes := applicableServersWithPrefixes(username, servers)
	for i, s := range applicable {
		func() {
			client, release, err := r.clientFor(ctx, s)
			if err != nil {
				slog.Warn("mcp: skipping server (connect failed)", "server", s.Name, "scope", s.Scope, "error", err)
				return
			}
			defer release()

			tools, err := client.ListTools(ctx)
			if err != nil {
				slog.Warn("mcp: skipping server (list tools failed)", "server", s.Name, "scope", s.Scope, "error", err)
				r.evictClient(s)
				return
			}

			prefix := prefixes[i] + "__"
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
		}()
	}

	return defs, nil
}

// toolHints returns one "Tool guide: <prefix>: <hint>" line per server
// applicable to username that has a non-empty Hint, using the same
// alias-or-name prefix toolsFor would assign. Pure/no network calls.
func (r *Registry) toolHints(username string, servers []Server) []string {
	applicable, prefixes := applicableServersWithPrefixes(username, servers)
	var lines []string
	for i, s := range applicable {
		if s.Hint == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("Tool guide: %s: %s", prefixes[i], s.Hint))
	}
	return lines
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
	prefix, realTool, ok := strings.Cut(toolName, "__")
	if !ok {
		return "", fmt.Errorf("mcp: invalid tool name %q (expected <server>__<tool>)", toolName)
	}

	s, ok := findApplicableServerIn(servers, username, prefix)
	if !ok {
		return "", fmt.Errorf("mcp: no server %q available for user %q", prefix, username)
	}

	if !s.allowsTool(realTool) {
		return "", fmt.Errorf("mcp: tool %q is not enabled for server %q", realTool, prefix)
	}

	client, release, err := r.clientFor(ctx, s)
	if err != nil {
		return "", fmt.Errorf("mcp: connect to server %s: %w", s.Name, err)
	}
	defer release()

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
	client, release, err := r.clientFor(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("mcp: probe connect %s/%s: %w", s.Name, s.Scope, err)
	}
	defer release()
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

// findApplicableServerIn finds the server whose effective tool-name prefix
// (alias, or its Name when no alias applies — see serverPrefixes) matches
// prefix case-insensitively, among servers applying to username. Searching
// env servers before the user's own DB servers (the order applicableServers
// returns them in) means on a name collision the env server wins, same as
// before aliasing existed.
func findApplicableServerIn(servers []Server, username, prefix string) (Server, bool) {
	applicable, prefixes := applicableServersWithPrefixes(username, servers)
	for i, s := range applicable {
		if strings.EqualFold(prefixes[i], prefix) {
			return s, true
		}
	}
	return Server{}, false
}

// applicableServersWithPrefixes filters servers down to those applying to
// username (preserving order) and computes each one's effective tool-name
// prefix via serverPrefixes, so callers get a servers/prefixes pair indexed
// in lockstep.
func applicableServersWithPrefixes(username string, servers []Server) ([]Server, []string) {
	applicable := make([]Server, 0, len(servers))
	for _, s := range servers {
		if s.AppliesTo(username) {
			applicable = append(applicable, s)
		}
	}
	return applicable, serverPrefixes(applicable)
}

// serverPrefixes computes the effective tool-name prefix for each server in
// servers (already filtered to one user's applicable servers): the
// lowercased Alias when set and non-colliding, otherwise the lowercased
// Name — exactly today's behavior for any server without an alias. A
// collision (an alias matching another server's Name, or an earlier
// server's already-assigned prefix) falls back to the server's own Name
// instead of crashing, with a warning logged.
func serverPrefixes(servers []Server) []string {
	reservedNames := make(map[string]bool, len(servers))
	for _, s := range servers {
		reservedNames[strings.ToLower(s.Name)] = true
	}

	used := make(map[string]bool, len(servers))
	prefixes := make([]string, len(servers))
	for i, s := range servers {
		name := strings.ToLower(s.Name)
		alias := strings.ToLower(s.Alias)

		prefix := name
		switch {
		case alias == "" || alias == name:
			// No alias, or alias redundant with the name: prefix is name.
		case used[alias] || reservedNames[alias]:
			slog.Warn("mcp: server alias collides, falling back to server name",
				"server", s.Name, "scope", s.Scope, "alias", s.Alias)
		default:
			prefix = alias
		}

		used[prefix] = true
		prefixes[i] = prefix
	}
	return prefixes
}

// clientFor returns a client for the given server plus a release func the
// caller must invoke when done with it.
//
// Env-configured servers use a lazily-created, cached client (keyed by
// Name+Scope), reference-counted via leasedClient: each hand-out takes a
// lease, and release drops it. The cache owns the client's lifetime as long
// as it's still cached, but eviction (on a transient failure) or
// Registry.Close only close it once every outstanding lease has been
// released — never out from under an in-flight ListTools/CallTool on the
// same shared client. User-defined (DB) servers always get a fresh client,
// because their credentials/URL can change or be revoked at any time; their
// release closes it directly, so a per-user dispatch does not leak the
// connection. Returning the release func keeps the env-vs-user distinction in
// this one place instead of duplicating it at every call site.
//
// The network dial (newClient's Start+Initialize handshake) never runs while
// holding r.mu: the mutex only ever guards the map get/set/delete, each held
// for the duration of a lookup or insert, never across I/O. On a cache miss,
// concurrent callers for the SAME key share one in-flight dial via
// r.dial (a singleflight group) — its result (client or error) is fanned out
// to every waiter — while callers for DIFFERENT keys dial fully in parallel.
// The shared dial itself runs on a context detached from whichever caller's
// call happened to start it (context.WithoutCancel), bounded instead by
// dialTimeout: without this, canceling the "leader" caller's context (e.g. an
// aborted chat stream) would abort the in-flight dial and hand every waiter
// sharing it a context.Canceled error unrelated to their own request.
func (r *Registry) clientFor(ctx context.Context, s Server) (mcpClient, func(), error) {
	noop := func() {}

	if !r.isEnvServer(s) {
		c, err := dialClient(ctx, s, r.httpClient)
		if err != nil {
			return nil, noop, err
		}
		return c, func() { _ = c.Close() }, nil
	}

	key := s.Name + "/" + s.Scope

	if c, release, ok := r.leaseCached(key); ok {
		return c, release, nil
	}

	// The lease record is created ONCE, inside the singleflight, so every
	// waiter in a fan-out shares the same record. Creating it per-waiter after
	// the dial is unsound: if the first waiter returns, fails its call and
	// evicts before a later waiter runs its insert, that later waiter finds an
	// empty map and caches a SECOND record wrapping the same client — while the
	// first record's release closes it. The cache then holds a closed transport.
	v, err, _ := r.dial.Do(key, func() (any, error) {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dialTimeout)
		defer cancel()
		c, dErr := dialClient(dctx, s, r.httpClient)
		if dErr != nil {
			return nil, dErr
		}
		return &leasedClient{client: c}, nil
	})
	if err != nil {
		return nil, noop, err
	}
	lc := v.(*leasedClient) //nolint:forcetypeassert // dial's fn always returns *leasedClient

	r.mu.Lock()
	if existing, ok := r.clients[key]; ok && existing != lc {
		// Another goroutine cached a different client for this key while we
		// dialed. Keep theirs and take a lease on it; discard ours.
		existing.leases++
		r.mu.Unlock()
		r.discard(lc)
		return existing.client, func() { r.release(existing) }, nil
	}
	if lc.evicted || r.closed {
		// Evicted (or the whole registry was closed) while this dial was
		// fanning out. Still usable for this caller, but must not be re-cached;
		// the last releaser closes it.
		lc.evicted = true
		lc.leases++
		r.mu.Unlock()
		return lc.client, func() { r.release(lc) }, nil
	}
	r.clients[key] = lc
	lc.leases++
	r.mu.Unlock()

	return lc.client, func() { r.release(lc) }, nil
}

// discard drops a dial result that lost a cache race, closing it if nobody took
// a lease on it.
func (r *Registry) discard(lc *leasedClient) {
	r.mu.Lock()
	lc.evicted = true
	shouldClose := lc.leases <= 0 && !lc.closed
	if shouldClose {
		lc.closed = true
	}
	r.mu.Unlock()
	if shouldClose {
		_ = lc.client.Close()
	}
}

// leaseCached takes a lease on key's cached client without dialing.
func (r *Registry) leaseCached(key string) (mcpClient, func(), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lc, ok := r.clients[key]
	if !ok {
		return nil, nil, false
	}
	lc.leases++
	return lc.client, func() { r.release(lc) }, true
}

// release drops one lease, closing the transport if it has been evicted and
// this was the last holder.
func (r *Registry) release(lc *leasedClient) {
	r.mu.Lock()
	lc.leases--
	shouldClose := lc.evicted && lc.leases <= 0 && !lc.closed
	if shouldClose {
		lc.closed = true
	}
	r.mu.Unlock()
	if shouldClose {
		_ = lc.client.Close()
	}
}

// evictClient drops s's cached client (if any) so the next clientFor call
// redials instead of reusing a connection that just failed a probe or tool
// call. Only env-configured servers are ever cached (see clientFor); calling
// this for a user-defined server is a harmless no-op — those already get a
// fresh client on every call. If the evicted client is still leased by
// another in-flight call, closing it is deferred to that caller's release —
// otherwise we would close a transport out from under it.
func (r *Registry) evictClient(s Server) {
	if !r.isEnvServer(s) {
		return
	}
	key := s.Name + "/" + s.Scope
	r.mu.Lock()
	lc, ok := r.clients[key]
	var shouldClose bool
	if ok {
		delete(r.clients, key)
		lc.evicted = true
		shouldClose = lc.leases <= 0 && !lc.closed
		if shouldClose {
			lc.closed = true
		}
	}
	r.mu.Unlock()
	if shouldClose {
		_ = lc.client.Close()
	}
}

// Close closes every cached client (deferring any still leased to its last
// release) and clears the cache. Safe to call more than once. Registered as a
// deferred call at construction time in cmd/server/serve.
func (r *Registry) Close() error {
	r.mu.Lock()
	// closed makes a dial that completes AFTER Close refuse to re-cache its
	// result, so it cannot leak past shutdown.
	r.closed = true
	var toClose []*leasedClient
	for key, lc := range r.clients {
		delete(r.clients, key)
		lc.evicted = true
		if lc.leases <= 0 && !lc.closed {
			lc.closed = true
			toClose = append(toClose, lc)
		}
	}
	r.mu.Unlock()

	var errs []error
	for _, lc := range toClose {
		if err := lc.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// isEnvServer reports whether s is one of the env-configured servers (vs. a
// per-user DB server supplied by userSrc). Provenance is read directly from
// s.FromEnv rather than inferred by matching Name+Scope against r.servers: a
// user-defined server can collide with an env server on both fields, and
// name-matching would misclassify it as env-configured.
func (r *Registry) isEnvServer(s Server) bool {
	return s.FromEnv
}
