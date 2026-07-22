package mcp

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
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
	OK        bool
	ToolCount int
	Tools     []ToolInfo
	Err       string
	CheckedAt time.Time
}

type healthSource interface {
	Servers() []Server
	Probe(ctx context.Context, s Server) ([]ToolInfo, error)
}

// HealthPoller periodically probes every configured MCP server and caches the
// latest health per (name, scope). Reads are cheap and never touch the network.
type HealthPoller struct {
	src          healthSource
	interval     time.Duration
	probeTimeout time.Duration
	mu           sync.RWMutex
	cache        map[string]ServerHealth // keyed Name+"/"+Scope
}

// NewHealthPoller builds a poller over src.
func NewHealthPoller(src healthSource, interval time.Duration) *HealthPoller {
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	return &HealthPoller{
		src: src, interval: interval, probeTimeout: defaultProbeTimeout,
		cache: make(map[string]ServerHealth),
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
			out = append(out, ServerHealth{Name: s.Name, Scope: s.Scope, Transport: s.Transport, URL: s.URL})
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
