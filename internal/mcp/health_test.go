package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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
			{Name: "garmin", Scope: scopeGlobal, Transport: "streamable-http", URL: "http://g", Alias: "g", Hint: "for garmin things"},
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
	if byName["garmin"].Alias != "g" || byName["garmin"].Hint != "for garmin things" {
		t.Fatalf("garmin health alias/hint = %#v, want g/for garmin things", byName["garmin"])
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

const hangServerName = "hang"

// hangingSource is a healthSource whose hangServerName server blocks until its probe
// ctx is cancelled (simulating an unreachable/hanging remote server), while
// every other named server responds immediately. probed records which
// servers actually got a Probe call, so the test can assert the hanging
// server didn't block the others.
type hangingSource struct {
	servers []Server
	probed  sync.Map // name -> struct{}
}

func (h *hangingSource) Servers() []Server { return h.servers }

func (h *hangingSource) Probe(ctx context.Context, s Server) ([]ToolInfo, error) {
	h.probed.Store(s.Name, struct{}{})
	if s.Name == hangServerName {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []ToolInfo{{Name: "t"}}, nil
}

// TestHealthPoller_HangingServerDoesNotBlockCycle verifies a per-probe
// timeout bounds one hanging/unreachable server so it can't stall the whole
// poll cycle, and that the other configured servers are still probed
// (concurrently) within that same cycle.
func TestHealthPoller_HangingServerDoesNotBlockCycle(t *testing.T) {
	src := &hangingSource{servers: []Server{
		{Name: hangServerName, Scope: scopeGlobal},
		{Name: "ok1", Scope: scopeGlobal},
		{Name: "ok2", Scope: scopeGlobal},
	}}
	p := NewHealthPoller(src, DefaultHealthInterval)
	p.probeTimeout = 50 * time.Millisecond // avoid a real 10s wait in this test

	start := time.Now()
	p.probeAll(context.Background())
	elapsed := time.Since(start)

	// The whole cycle must complete close to the (short) per-probe timeout,
	// not hang indefinitely waiting on the hang server.
	if elapsed > time.Second {
		t.Fatalf("probeAll took %s, want bounded by probeTimeout (%s)", elapsed, p.probeTimeout)
	}

	for _, name := range []string{hangServerName, "ok1", "ok2"} {
		if _, ok := src.probed.Load(name); !ok {
			t.Fatalf("server %q was never probed", name)
		}
	}

	st := p.StatusFor("anyone")
	byName := map[string]ServerHealth{}
	for _, h := range st {
		byName[h.Name] = h
	}
	if byName[hangServerName].OK || byName[hangServerName].Err == "" {
		t.Fatalf("hang health = %#v, want !OK w/ a timeout error", byName[hangServerName])
	}
	if !byName["ok1"].OK || !byName["ok2"].OK {
		t.Fatalf("ok1/ok2 health = %#v/%#v, want both OK despite hang", byName["ok1"], byName["ok2"])
	}
}
