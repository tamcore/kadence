package mcp

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// DefaultHealthInterval is how often the poller probes each configured server.
const DefaultHealthInterval = 45 * time.Second

// defaultProbeTimeout bounds a single server probe, so one hanging/
// unreachable server can never stall the whole poll cycle (or eat into the
// next one). Overridable per-poller via HealthPoller.probeTimeout (e.g. in
// tests, to avoid a real 10s wait).
const defaultProbeTimeout = 10 * time.Second

// maxConcurrentProbes bounds how many servers are probed at once, so a large
// fleet of configured servers doesn't open unbounded concurrent connections.
const maxConcurrentProbes = 4

// ServerHealth is the latest probe result for one configured server.
type ServerHealth struct {
	Name      string
	Scope     string
	Transport string
	URL       string
	Alias     string
	Hint      string
	OK        bool
	ToolCount int
	Tools     []ToolInfo
	Err       string
	CheckedAt time.Time
}

type healthSource interface {
	Servers() []Server
	Probe(ctx context.Context, s Server) ([]ToolInfo, error)
	ProbeFor(ctx context.Context, s Server, username string) ([]ToolInfo, error)
}

// HealthPoller periodically probes every configured MCP server and caches the
// latest health per (name, scope). Reads are cheap and never touch the network.
type HealthPoller struct {
	src          healthSource
	interval     time.Duration
	probeTimeout time.Duration
	mu           sync.RWMutex
	cache        map[string]ServerHealth // keyed Name+"/"+Scope
	// principals holds per-user probes of per-principal servers, which the
	// shared cache above cannot represent. Keyed Name+"/"+Scope+NUL+username.
	principals      map[string]ServerHealth
	principalFlight singleflight.Group
}

// NewHealthPoller builds a poller over src.
func NewHealthPoller(src healthSource, interval time.Duration) *HealthPoller {
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	return &HealthPoller{
		src: src, interval: interval, probeTimeout: defaultProbeTimeout,
		cache: make(map[string]ServerHealth), principals: make(map[string]ServerHealth),
	}
}

// Run probes immediately, then every interval, until ctx is cancelled.
func (p *HealthPoller) Run(ctx context.Context) {
	p.probeAll(ctx)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.probeAll(ctx)
		}
	}
}

// probeAll probes every configured server once and replaces its cache entry.
// Probes run concurrently (bounded by maxConcurrentProbes) and each is capped
// by probeTimeout, so a single hanging/unreachable server can neither stall
// the rest of the cycle nor block indefinitely.
func (p *HealthPoller) probeAll(ctx context.Context) {
	start := time.Now()
	servers := p.src.Servers()

	var g errgroup.Group
	g.SetLimit(maxConcurrentProbes)

	for _, s := range servers {
		// A per-principal server has no deployment-wide credential, so a probe
		// could only ever fail. Reporting that as unhealthy would libel a server
		// that works for every linked user; its real state is each user's own
		// link, shown in Settings.
		if s.PerPrincipal() {
			continue
		}
		g.Go(func() error {
			p.probeOne(ctx, s)
			return nil
		})
	}
	// probeOne never returns an error (failures are recorded in the cache);
	// Wait simply blocks until every probe (or its own timeout) completes.
	_ = g.Wait()

	slog.Debug("mcp: health poll cycle complete",
		"servers", len(servers), "duration", time.Since(start))
}

// probeOne probes a single server with a bounded timeout and replaces its
// cache entry with the result.
func (p *HealthPoller) probeOne(ctx context.Context, s Server) {
	probeCtx, cancel := context.WithTimeout(ctx, p.probeTimeout)
	defer cancel()

	h := ServerHealth{
		Name: s.Name, Scope: s.Scope, Transport: s.Transport, URL: s.URL,
		Alias: s.Alias, Hint: s.Hint,
		CheckedAt: time.Now(),
	}
	tools, err := p.src.Probe(probeCtx, s)
	if err != nil {
		h.OK = false
		h.Err = err.Error()
	} else {
		h.OK = true
		h.Tools = tools
		h.ToolCount = len(tools)
	}
	p.mu.Lock()
	p.cache[s.Name+"/"+s.Scope] = h
	p.mu.Unlock()
}

// StatusFor returns cached health for every configured server applicable to
// username, in configured order. A not-yet-probed server has a zero CheckedAt.
func (p *HealthPoller) StatusFor(username string) []ServerHealth {
	servers := p.src.Servers()

	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []ServerHealth
	for _, s := range servers {
		if !s.AppliesTo(username) {
			continue
		}
		if h, ok := p.cache[s.Name+"/"+s.Scope]; ok {
			out = append(out, h)
		} else {
			out = append(out, ServerHealth{
				Name: s.Name, Scope: s.Scope, Transport: s.Transport, URL: s.URL,
				Alias: s.Alias, Hint: s.Hint,
			})
		}
	}
	return out
}

// ToolsFor returns cached tools for one server applicable to username
// (case-insensitive name match). ok is false when the server is not applicable.
func (p *HealthPoller) ToolsFor(username, serverName string) ([]ToolInfo, bool) {
	servers := p.src.Servers()

	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range servers {
		if !strings.EqualFold(s.Name, serverName) || !s.AppliesTo(username) {
			continue
		}
		if h, ok := p.cache[s.Name+"/"+s.Scope]; ok {
			return h.Tools, true
		}
		return nil, true // applicable but not yet probed
	}
	return nil, false
}

// principalProbeTimeout bounds a per-user probe made while serving a request.
// It is shorter than the background probe timeout because a person is waiting
// on the page, and a slow server should degrade to "unavailable" rather than
// hold the whole response.
const principalProbeTimeout = 5 * time.Second

// principalCacheTTL is how long one user's probe result is reused.
//
// It exists because the app shell polls the status endpoint every ten seconds
// in every open tab. Probing live on each of those would dial the remote
// server permanently, for every signed-in user, to answer a question whose
// answer almost never changes. The one moment it does change — linking or
// unlinking — invalidates the entry explicitly, so the page still reflects a
// new link immediately rather than up to a TTL later.
const principalCacheTTL = 30 * time.Second

// StatusForPrincipal is StatusFor with per-principal servers resolved on
// username's behalf.
//
// A per-principal server is absent from the background cache by construction:
// its credential belongs to one user, so the poller has nothing to probe with
// and records it as unhealthy with zero tools for everyone. Serving that would
// tell a freshly linked user their integration is empty.
func (p *HealthPoller) StatusForPrincipal(ctx context.Context, username string) []ServerHealth {
	out := p.StatusFor(username)
	servers := p.src.Servers()
	for i, h := range out {
		s, ok := findServer(servers, h.Name, h.Scope)
		if !ok || !s.PerPrincipal() {
			continue
		}
		out[i], _ = p.probeAsPrincipal(ctx, s, username)
	}
	return out
}

// ToolsForPrincipal is ToolsFor with a per-principal server resolved on
// username's behalf. The error is non-nil only when the probe itself failed,
// which the caller must distinguish from a server that legitimately exposes
// nothing.
func (p *HealthPoller) ToolsForPrincipal(
	ctx context.Context, username, serverName string,
) ([]ToolInfo, bool, error) {
	for _, s := range p.src.Servers() {
		if !strings.EqualFold(s.Name, serverName) || !s.AppliesTo(username) {
			continue
		}
		if !s.PerPrincipal() {
			tools, ok := p.ToolsFor(username, serverName)
			return tools, ok, nil
		}
		h, err := p.probeAsPrincipal(ctx, s, username)
		return h.Tools, true, err
	}
	return nil, false, nil
}

// InvalidatePrincipal drops one user's cached probe for serverID, so the next
// read reflects a link or unlink that just happened instead of waiting out the
// TTL. serverID is matched case-insensitively against the server name.
func (p *HealthPoller) InvalidatePrincipal(username, serverID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key := range p.principals {
		name, user, ok := splitPrincipalKey(key)
		if ok && user == username && strings.EqualFold(name, serverID) {
			delete(p.principals, key)
		}
	}
}

// probeAsPrincipal returns username's view of s, from cache when fresh.
//
// A failure is returned as an unhealthy status AND an error: the page renders
// "not linked" from the status, while a caller that must tell an outage from
// an empty server reads the error.
func (p *HealthPoller) probeAsPrincipal(ctx context.Context, s Server, username string) (ServerHealth, error) {
	key := principalKey(s, username)

	p.mu.RLock()
	cached, ok := p.principals[key]
	p.mu.RUnlock()
	if ok && time.Since(cached.CheckedAt) < principalCacheTTL {
		return cached, nil
	}

	// Deduplicated: several tabs polling at once produce one dial, and all of
	// them get its result.
	v, err, _ := p.principalFlight.Do(key, func() (any, error) {
		probeCtx, cancel := context.WithTimeout(ctx, principalProbeTimeout)
		defer cancel()

		h := ServerHealth{
			Name: s.Name, Scope: s.Scope, Transport: s.Transport, URL: s.URL,
			Alias: s.Alias, Hint: s.Hint, CheckedAt: time.Now(),
		}
		tools, probeErr := p.src.ProbeFor(probeCtx, s, username)
		if probeErr != nil {
			h.Err = probeErr.Error()
		} else {
			h.OK, h.Tools, h.ToolCount = true, tools, len(tools)
		}

		p.mu.Lock()
		p.principals[key] = h
		p.mu.Unlock()
		return h, probeErr
	})
	h, _ := v.(ServerHealth)
	return h, err
}

// principalKey and splitPrincipalKey bracket the cache key encoding. The
// username goes last because a server name cannot contain the separator but a
// username might.
func principalKey(s Server, username string) string {
	return s.Name + "/" + s.Scope + "\x00" + username
}

func splitPrincipalKey(key string) (nameScope, username string, ok bool) {
	before, after, ok0 := strings.Cut(key, "\x00")
	if !ok0 {
		return "", "", false
	}
	name, _, _ := strings.Cut(before, "/")
	return name, after, true
}

// findServer locates a server by name and scope in an already-taken snapshot,
// so one request never re-enumerates every user's servers per entry.
func findServer(servers []Server, name, scope string) (Server, bool) {
	for _, s := range servers {
		if s.Name == name && s.Scope == scope {
			return s, true
		}
	}
	return Server{}, false
}
