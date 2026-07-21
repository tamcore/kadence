package mcp

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct {
	servers []Server
	fail    map[string]bool
}

func (f *fakeSource) Servers() []Server { return f.servers }
func (f *fakeSource) Probe(_ context.Context, s Server) ([]ToolInfo, error) {
	if f.fail[s.Name] {
		return nil, errors.New("down")
	}
	return []ToolInfo{{Name: "t1"}, {Name: "t2"}}, nil
}

func TestHealthPoller_ProbeAllAndStatusFor(t *testing.T) {
	src := &fakeSource{
		servers: []Server{
			{Name: "garmin", Scope: scopeGlobal, Transport: "streamable-http", URL: "http://g"},
			{Name: "priv", Scope: "USER_alice", Transport: "sse", URL: "http://p"},
			{Name: "down", Scope: scopeGlobal, URL: "http://d"},
		},
		fail: map[string]bool{"down": true},
	}
	p := NewHealthPoller(src, DefaultHealthInterval)
	p.probeAll(context.Background()) // one cycle, synchronous

	// alice sees GLOBAL garmin + GLOBAL down + her USER_alice priv = 3
	st := p.StatusFor("alice")
	if len(st) != 3 {
		t.Fatalf("StatusFor(alice) = %d servers, want 3", len(st))
	}
	// bob sees only the two GLOBAL servers
	if len(p.StatusFor("bob")) != 2 {
		t.Fatalf("StatusFor(bob) = %d, want 2", len(p.StatusFor("bob")))
	}
	byName := map[string]ServerHealth{}
	for _, s := range st {
		byName[s.Name] = s
	}
	if !byName["garmin"].OK || byName["garmin"].ToolCount != 2 {
		t.Fatalf("garmin health = %#v, want OK w/ 2 tools", byName["garmin"])
	}
	if byName["down"].OK || byName["down"].Err == "" {
		t.Fatalf("down health = %#v, want !OK w/ error", byName["down"])
	}

	// ToolsFor: applicable server returns tools; non-applicable returns false.
	if tools, ok := p.ToolsFor("alice", "priv"); !ok || len(tools) != 2 {
		t.Fatalf("ToolsFor(alice, priv) = %v,%v want 2 tools,true", len(tools), ok)
	}
	if _, ok := p.ToolsFor("bob", "priv"); ok {
		t.Fatal("ToolsFor(bob, priv) ok=true, want false (not applicable)")
	}
}

func TestHealthPoller_RunStopsOnCancel(t *testing.T) {
	src := &fakeSource{servers: []Server{{Name: "g", Scope: scopeGlobal}}}
	p := NewHealthPoller(src, DefaultHealthInterval)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx) // must return promptly (one immediate probe then ctx done)
}
