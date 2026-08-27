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

func (f *fakeSource) ProbeFor(ctx context.Context, s Server, _ string) ([]ToolInfo, error) {
	return f.Probe(ctx, s)
}

func (f *fakeSource) Probe(_ context.Context, s Server) ([]ToolInfo, error) {
	if f.fail[s.Name] {
		return nil, errors.New("down")
	}
	return []ToolInfo{{Name: "t1"}, {Name: "t2"}}, nil
}

func TestHealthPoller_ProbeAllAndStatusFor(t *testing.T) {
	src := &fakeSource{
		servers: []Server{
			{Name: testGarminAlias, Scope: scopeGlobal, Transport: "streamable-http", URL: "http://g", Alias: "g", Hint: "for garmin things"},
			{Name: "priv", Scope: "USER_alice", Transport: "sse", URL: "http://p"},
			{Name: "down", Scope: scopeGlobal, URL: "http://d"},
		},
		fail: map[string]bool{"down": true},
	}
	p := NewHealthPoller(src, DefaultHealthInterval)
	p.probeAll(t.Context()) // one cycle, synchronous

	// alice sees GLOBAL garmin + GLOBAL down + her USER_alice priv = 3
	st := p.StatusFor(testUsername)
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
	if !byName[testGarminAlias].OK || byName[testGarminAlias].ToolCount != 2 {
		t.Fatalf("garmin health = %#v, want OK w/ 2 tools", byName[testGarminAlias])
	}
	if byName[testGarminAlias].Alias != "g" || byName[testGarminAlias].Hint != "for garmin things" {
		t.Fatalf("garmin health alias/hint = %#v, want g/for garmin things", byName[testGarminAlias])
	}
	if byName["down"].OK || byName["down"].Err == "" {
		t.Fatalf("down health = %#v, want !OK w/ error", byName["down"])
	}

	// ToolsFor: applicable server returns tools; non-applicable returns false.
	if tools, ok := p.ToolsFor(testUsername, "priv"); !ok || len(tools) != 2 {
		t.Fatalf("ToolsFor(alice, priv) = %v,%v want 2 tools,true", len(tools), ok)
	}
	if _, ok := p.ToolsFor("bob", "priv"); ok {
		t.Fatal("ToolsFor(bob, priv) ok=true, want false (not applicable)")
	}
}

func TestHealthPoller_RunStopsOnCancel(t *testing.T) {
	src := &fakeSource{servers: []Server{{Name: "g", Scope: scopeGlobal}}}
	p := NewHealthPoller(src, DefaultHealthInterval)
	ctx, cancel := context.WithCancel(t.Context())
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

func (h *hangingSource) ProbeFor(ctx context.Context, s Server, _ string) ([]ToolInfo, error) {
	return h.Probe(ctx, s)
}

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
	p.probeAll(t.Context())
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

// principalProbeSource records which probe path the poller took.
type principalProbeSource struct {
	mu         sync.Mutex
	servers    []Server
	sharedHits int
	userHits   map[string]int
	tools      []ToolInfo
	perUser    map[string][]ToolInfo
	err        error
}

func (s *principalProbeSource) Servers() []Server { return s.servers }

func (s *principalProbeSource) Probe(context.Context, Server) ([]ToolInfo, error) {
	s.sharedHits++
	return nil, ErrProbeNeedsPrincipal
}

func (s *principalProbeSource) ProbeFor(_ context.Context, _ Server, username string) ([]ToolInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userHits == nil {
		s.userHits = map[string]int{}
	}
	s.userHits[username]++
	if s.err != nil {
		return nil, s.err
	}
	if per, ok := s.perUser[username]; ok {
		return per, nil
	}
	return s.tools, nil
}

func (s *principalProbeSource) hits(username string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userHits[username]
}

func perPrincipalServer() Server {
	return Server{
		Name: "GARMIN", Scope: scopeGlobal, URL: "https://garmin.invalid/mcp",
		Transport: transportStreamableHTTP, AuthMode: authModeOAuth, FromEnv: true,
	}
}

func TestStatusForAPrincipalReportsThatUsersToolCount(t *testing.T) {
	// The cached deployment-wide probe can never see a per-principal server's
	// tools, so serving it would tell a linked user they have none.
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		tools:   []ToolInfo{{Name: testGetActivities}, {Name: "count_activities"}},
	}
	p := NewHealthPoller(src, time.Minute)

	got := p.StatusForPrincipal(t.Context(), "alice")

	if len(got) != 1 {
		t.Fatalf("got %d statuses, want 1", len(got))
	}
	if !got[0].OK {
		t.Fatalf("a linked user's server reported unhealthy: %q", got[0].Err)
	}
	if got[0].ToolCount != 2 {
		t.Fatalf("ToolCount = %d, want 2", got[0].ToolCount)
	}
	if src.hits("alice") != 1 {
		t.Fatalf("the per-user probe ran %d times, want 1", src.hits("alice"))
	}
}

func TestStatusForAPrincipalReportsAnUnlinkedUserAsNotOK(t *testing.T) {
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		err:     errors.New("not linked"),
	}
	p := NewHealthPoller(src, time.Minute)

	got := p.StatusForPrincipal(t.Context(), "alice")

	if len(got) != 1 {
		t.Fatalf("got %d statuses, want 1", len(got))
	}
	if got[0].OK {
		t.Fatal("an unlinked user's server reported healthy")
	}
	if got[0].ToolCount != 0 {
		t.Fatalf("ToolCount = %d, want 0", got[0].ToolCount)
	}
}

func TestTwoUsersSeeTheirOwnToolCounts(t *testing.T) {
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		tools:   []ToolInfo{{Name: testGetActivities}},
	}
	p := NewHealthPoller(src, time.Minute)

	_ = p.StatusForPrincipal(t.Context(), "alice")
	_ = p.StatusForPrincipal(t.Context(), "bob")

	if src.hits("alice") != 1 || src.hits("bob") != 1 {
		t.Fatalf("probes were not per user: alice=%d bob=%d", src.hits("alice"), src.hits("bob"))
	}
}

func TestOneUsersToolsAreNeverServedToAnother(t *testing.T) {
	// The cache is keyed per user. A key collision, or reuse of a shared
	// entry, would hand one person another person's capability list.
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		perUser: map[string][]ToolInfo{
			"alice": {{Name: testGetActivities}},
			"bob":   {{Name: "count_activities"}, {Name: "get_sleep_data"}},
		},
	}
	p := NewHealthPoller(src, time.Minute)

	var wg sync.WaitGroup
	got := map[string][]ToolInfo{}
	var mu sync.Mutex
	for _, user := range []string{"alice", "bob"} {
		wg.Go(func() {
			tools, _, err := p.ToolsForPrincipal(t.Context(), user, "garmin")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("ToolsForPrincipal(%s): %v", user, err)
			}
			got[user] = tools
		})
	}
	wg.Wait()

	if len(got["alice"]) != 1 || got["alice"][0].Name != testGetActivities {
		t.Fatalf("alice saw %v, want her own single tool", got["alice"])
	}
	if len(got["bob"]) != 2 {
		t.Fatalf("bob saw %v, want his own two tools", got["bob"])
	}
}

func TestAFreshProbeIsReusedWithinTheTTL(t *testing.T) {
	// The app shell polls this endpoint every ten seconds in every open tab.
	// Dialling the remote server each time would be permanent traffic to
	// answer a question whose answer almost never changes.
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		tools:   []ToolInfo{{Name: testGetActivities}},
	}
	p := NewHealthPoller(src, time.Minute)

	for range 5 {
		_ = p.StatusForPrincipal(t.Context(), "alice")
	}

	if n := src.hits("alice"); n != 1 {
		t.Fatalf("%d probes for five polls, want 1", n)
	}
}

func TestLinkingInvalidatesThatUsersCachedProbe(t *testing.T) {
	// Without this the page would keep showing "not connected" for up to the
	// TTL after someone links, which is exactly the moment they are looking.
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		err:     errors.New("not linked"),
	}
	p := NewHealthPoller(src, time.Minute)

	if got := p.StatusForPrincipal(t.Context(), "alice"); got[0].OK {
		t.Fatal("an unlinked user reported healthy")
	}

	src.mu.Lock()
	src.err = nil
	src.tools = []ToolInfo{{Name: testGetActivities}}
	src.mu.Unlock()
	p.InvalidatePrincipal("alice", "garmin")

	got := p.StatusForPrincipal(t.Context(), "alice")
	if !got[0].OK || got[0].ToolCount != 1 {
		t.Fatalf("after linking: OK=%v tools=%d, want healthy with 1", got[0].OK, got[0].ToolCount)
	}
}

func TestInvalidatingOneUserLeavesAnothersCacheAlone(t *testing.T) {
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		tools:   []ToolInfo{{Name: testGetActivities}},
	}
	p := NewHealthPoller(src, time.Minute)
	_ = p.StatusForPrincipal(t.Context(), "alice")
	_ = p.StatusForPrincipal(t.Context(), "bob")

	p.InvalidatePrincipal("alice", "garmin")
	_ = p.StatusForPrincipal(t.Context(), "alice")
	_ = p.StatusForPrincipal(t.Context(), "bob")

	if src.hits("alice") != 2 {
		t.Fatalf("alice probed %d times, want 2", src.hits("alice"))
	}
	if src.hits("bob") != 1 {
		t.Fatalf("bob was re-probed %d times, want 1", src.hits("bob"))
	}
}

func TestToolsForAPrincipalReturnsThatUsersTools(t *testing.T) {
	src := &principalProbeSource{
		servers: []Server{perPrincipalServer()},
		tools:   []ToolInfo{{Name: testGetActivities}},
	}
	p := NewHealthPoller(src, time.Minute)

	tools, ok, err := p.ToolsForPrincipal(t.Context(), "alice", "garmin")
	if err != nil {
		t.Fatalf("ToolsForPrincipal: %v", err)
	}

	if !ok {
		t.Fatal("the server was reported as not applicable")
	}
	if len(tools) != 1 || tools[0].Name != testGetActivities {
		t.Fatalf("tools = %v, want the user's own list", tools)
	}
}
