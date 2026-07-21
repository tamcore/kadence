package mcp

import (
	"context"
	"strings"
	"sync"
	"time"
)

// DefaultHealthInterval is how often the poller probes each configured server.
const DefaultHealthInterval = 45 * time.Second

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
	src      healthSource
	interval time.Duration
	mu       sync.RWMutex
	cache    map[string]ServerHealth // keyed Name+"/"+Scope
}

// NewHealthPoller builds a poller over src.
func NewHealthPoller(src healthSource, interval time.Duration) *HealthPoller {
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	return &HealthPoller{src: src, interval: interval, cache: make(map[string]ServerHealth)}
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
func (p *HealthPoller) probeAll(ctx context.Context) {
	for _, s := range p.src.Servers() {
		h := ServerHealth{
			Name: s.Name, Scope: s.Scope, Transport: s.Transport, URL: s.URL,
			CheckedAt: time.Now(),
		}
		tools, err := p.src.Probe(ctx, s)
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
}

// StatusFor returns cached health for every configured server applicable to
// username, in configured order. A not-yet-probed server has a zero CheckedAt.
func (p *HealthPoller) StatusFor(username string) []ServerHealth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []ServerHealth
	for _, s := range p.src.Servers() {
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
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.src.Servers() {
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
