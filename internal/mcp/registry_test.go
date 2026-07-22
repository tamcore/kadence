package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	testGarminName       = "GARMIN"
	testUserPhilippScope = userScopePrefix + "philipp"
	testGetToolsPattern  = "get_*"
)

// anonymizedActivitiesFixture is modeled on a real garmin_mcp get_activities
// response, fully anonymized (no real personal data). Verbatim per the phase
// 7a plan's "Anonymized MCP fixture".
const anonymizedActivitiesFixture = `{ "start": 0, "limit": 1, "count": 1, "has_more": false, "next_start": 1,
  "activities": [ { "id": 1001, "name": "Morning Run", "type": "running",
    "event_type": "uncategorized", "start_time": "2026-01-15 08:00:00",
    "distance_meters": 10000.0, "duration_seconds": 3000.0, "moving_duration_seconds": 2950.0,
    "calories": 700.0, "avg_hr_bpm": 150.0, "max_hr_bpm": 180.0, "steps": 8000,
    "elevation_gain_meters": 100.0, "elevation_loss_meters": 100.0,
    "owner_display_name": "test-user-0001" } ] }`

// newFakeGarminServer stands up a real mcp-go MCP server (streamable-http
// transport) over httptest, registering a get_activities tool that returns
// the anonymized fixture as text content. This exercises the real
// client<->server MCP handshake (initialize, list, call) rather than a stub.
func newFakeGarminServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := mcpserver.NewMCPServer("fake-garmin", "0.0.1")
	tool := mcpgo.NewTool("get_activities",
		mcpgo.WithDescription("Get activities with pagination support."),
		mcpgo.WithNumber("start", mcpgo.Description("start offset"), mcpgo.DefaultNumber(0)),
		mcpgo.WithNumber("limit", mcpgo.Description("page size"), mcpgo.DefaultNumber(20)),
	)
	srv.AddTool(tool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(anonymizedActivitiesFixture), nil
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(httpSrv)
	t.Cleanup(ts.Close)
	return ts
}

// newFakeGarminServerWithWorkouts is like newFakeGarminServer but additionally
// registers a create_run_workout tool, so tests can assert that a Tools glob
// filter distinguishes between multiple exposed tools.
func newFakeGarminServerWithWorkouts(t *testing.T) *httptest.Server {
	t.Helper()

	srv := mcpserver.NewMCPServer("fake-garmin", "0.0.1")
	tool := mcpgo.NewTool("get_activities",
		mcpgo.WithDescription("Get activities with pagination support."),
		mcpgo.WithNumber("start", mcpgo.Description("start offset"), mcpgo.DefaultNumber(0)),
		mcpgo.WithNumber("limit", mcpgo.Description("page size"), mcpgo.DefaultNumber(20)),
	)
	srv.AddTool(tool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(anonymizedActivitiesFixture), nil
	})

	workoutTool := mcpgo.NewTool("create_run_workout",
		mcpgo.WithDescription("Create a run workout."),
	)
	srv.AddTool(workoutTool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`{"status":"created"}`), nil
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(httpSrv)
	t.Cleanup(ts.Close)
	return ts
}

func TestRegistry_ToolsForAndCall(t *testing.T) {
	ts := newFakeGarminServer(t)

	reg := NewRegistry([]Server{
		{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP},
	}, nil, nil)
	if !reg.Enabled() {
		t.Fatal("registry with servers must be Enabled")
	}

	ctx := context.Background()

	tools, err := reg.ToolsFor(ctx, "anyuser")
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d: %+v", len(tools), tools)
	}
	if tools[0].Name != "garmin__get_activities" {
		t.Fatalf("want namespaced tool name garmin__get_activities, got %q", tools[0].Name)
	}
	if len(tools[0].Parameters) == 0 {
		t.Fatal("want non-empty Parameters schema")
	}

	result, err := reg.Call(ctx, "anyuser", "garmin__get_activities", `{"limit":1}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !strings.Contains(result, "Morning Run") {
		t.Fatalf("result missing 'Morning Run': %s", result)
	}
	if !strings.Contains(result, "activities") {
		t.Fatalf("result missing 'activities': %s", result)
	}
}

func TestRegistry_UserScopeIsolation(t *testing.T) {
	ts := newFakeGarminServer(t)

	reg := NewRegistry([]Server{
		{Name: testGarminName, Scope: testUserPhilippScope, URL: ts.URL, Transport: transportStreamableHTTP},
	}, nil, nil)

	ctx := context.Background()

	tools, err := reg.ToolsFor(ctx, "philipp")
	if err != nil {
		t.Fatalf("ToolsFor(philipp): %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool for philipp, got %d", len(tools))
	}

	tools, err = reg.ToolsFor(ctx, "someoneelse")
	if err != nil {
		t.Fatalf("ToolsFor(someoneelse): %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("USER_philipp server must not appear for someoneelse, got %d tools", len(tools))
	}

	if _, err := reg.Call(ctx, "someoneelse", "garmin__get_activities", `{}`); err == nil {
		t.Fatal("Call for a non-applicable user must error")
	}
}

func TestRegistry_EnabledFalseWhenNoServers(t *testing.T) {
	reg := NewRegistry(nil, nil, nil)
	if reg.Enabled() {
		t.Fatal("registry with no servers must not be Enabled")
	}
}

func TestRegistry_CallUnknownTool(t *testing.T) {
	ts := newFakeGarminServer(t)
	reg := NewRegistry([]Server{
		{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP},
	}, nil, nil)
	ctx := context.Background()

	if _, err := reg.Call(ctx, "anyuser", "unknownserver__whatever", `{}`); err == nil {
		t.Fatal("Call with unknown server prefix must error")
	}
}

func TestRegistry_ToolsForFiltersByTools(t *testing.T) {
	ts := newFakeGarminServerWithWorkouts(t)

	reg := NewRegistry([]Server{
		{
			Name:      testGarminName,
			Scope:     scopeGlobal,
			URL:       ts.URL,
			Transport: transportStreamableHTTP,
			Tools:     []string{testGetToolsPattern},
		},
	}, nil, nil)

	ctx := context.Background()
	tools, err := reg.ToolsFor(ctx, "anyuser")
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool (get_activities), got %d: %+v", len(tools), tools)
	}
	if tools[0].Name != "garmin__get_activities" {
		t.Fatalf("want garmin__get_activities, got %q", tools[0].Name)
	}
	for _, tl := range tools {
		if tl.Name == "garmin__create_run_workout" {
			t.Fatalf("create_run_workout must be filtered out, got tools: %+v", tools)
		}
	}
}

func TestRegistry_ServersAndProbe(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP}
	reg := NewRegistry([]Server{s}, nil, nil)

	if got := reg.Servers(); len(got) != 1 || got[0].Name != testGarminName {
		t.Fatalf("Servers() = %#v, want one %s server", got, testGarminName)
	}

	tools, err := reg.Probe(t.Context(), s)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("Probe returned no tools, want the fake server's tools")
	}
}

func TestRegistry_ProbeFiltersByTools(t *testing.T) {
	ts := newFakeGarminServerWithWorkouts(t)
	s := Server{
		Name:      testGarminName,
		Scope:     scopeGlobal,
		URL:       ts.URL,
		Transport: transportStreamableHTTP,
		Tools:     []string{testGetToolsPattern},
	}
	reg := NewRegistry([]Server{s}, nil, nil)

	tools, err := reg.Probe(t.Context(), s)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "get_activities" {
		t.Fatalf("Probe() = %+v, want only get_activities", tools)
	}
}

func TestRegistry_CallRejectsFilteredOutTool(t *testing.T) {
	ts := newFakeGarminServerWithWorkouts(t)

	reg := NewRegistry([]Server{
		{
			Name:      testGarminName,
			Scope:     scopeGlobal,
			URL:       ts.URL,
			Transport: transportStreamableHTTP,
			Tools:     []string{testGetToolsPattern},
		},
	}, nil, nil)

	ctx := context.Background()
	_, err := reg.Call(ctx, "anyuser", "garmin__create_run_workout", `{}`)
	if err == nil {
		t.Fatal("Call for a tool filtered out by Tools must error")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("want 'not enabled' error, got: %v", err)
	}
}

// fakeUserSrc implements UserServerSource for tests, mapping usernames to
// their DB-backed MCP servers.
type fakeUserSrc struct {
	perUser map[string][]Server
}

func (f *fakeUserSrc) ServersForUser(_ context.Context, u string) ([]Server, error) {
	return f.perUser[u], nil
}

func (f *fakeUserSrc) AllServers(_ context.Context) ([]Server, error) {
	var all []Server
	for _, s := range f.perUser {
		all = append(all, s...)
	}
	return all, nil
}

// newFakeGarminServerToggleable is like newFakeGarminServer, but requests
// can be made to fail (500) on demand via the returned *atomic.Bool, to
// exercise client-eviction-on-failure without tearing down the server.
func newFakeGarminServerToggleable(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()

	srv := mcpserver.NewMCPServer("fake-garmin", "0.0.1")
	tool := mcpgo.NewTool("get_activities", mcpgo.WithDescription("Get activities."))
	srv.AddTool(tool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(anonymizedActivitiesFixture), nil
	})
	httpSrv := mcpserver.NewStreamableHTTPServer(srv)

	var fail atomic.Bool
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		httpSrv.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, &fail
}

// TestRegistry_EvictsClientOnProbeFailure verifies that when a cached
// env-server client's probe fails, the registry drops it from the client
// cache so the next use redials instead of reusing the broken connection —
// and that redial succeeds once the server recovers.
func TestRegistry_EvictsClientOnProbeFailure(t *testing.T) {
	ts, fail := newFakeGarminServerToggleable(t)
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP}
	reg := NewRegistry([]Server{s}, nil, nil)
	ctx := context.Background()

	if _, err := reg.Probe(ctx, s); err != nil {
		t.Fatalf("initial Probe: %v", err)
	}
	if n := len(reg.clients); n != 1 {
		t.Fatalf("clients cached = %d, want 1 after a successful probe", n)
	}

	fail.Store(true)
	if _, err := reg.Probe(ctx, s); err == nil {
		t.Fatal("Probe over a broken connection must return an error")
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("clients cached = %d, want 0 (evicted) after a failed probe", n)
	}

	fail.Store(false)
	tools, err := reg.Probe(ctx, s)
	if err != nil {
		t.Fatalf("Probe after recovery: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("Probe after recovery returned no tools, want the redialed client's tools")
	}
	if n := len(reg.clients); n != 1 {
		t.Fatalf("clients cached = %d, want 1 after redial", n)
	}
}

// TestRegistry_EvictsClientOnCallFailure mirrors
// TestRegistry_EvictsClientOnProbeFailure for the Call path (a tool-call
// failure, not just a health-probe failure, must also evict the client).
func TestRegistry_EvictsClientOnCallFailure(t *testing.T) {
	ts, fail := newFakeGarminServerToggleable(t)
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP}
	reg := NewRegistry([]Server{s}, nil, nil)
	ctx := context.Background()

	if _, err := reg.Call(ctx, "anyuser", "garmin__get_activities", `{}`); err != nil {
		t.Fatalf("initial Call: %v", err)
	}
	if n := len(reg.clients); n != 1 {
		t.Fatalf("clients cached = %d, want 1 after a successful call", n)
	}

	fail.Store(true)
	if _, err := reg.Call(ctx, "anyuser", "garmin__get_activities", `{}`); err == nil {
		t.Fatal("Call over a broken connection must return an error")
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("clients cached = %d, want 0 (evicted) after a failed call", n)
	}
}

// countingUserSrc wraps fakeUserSrc, counting ServersForUser calls so tests
// can assert a chat turn resolves user servers exactly once (not once per
// tool call), via Registry.SnapshotFor.
type countingUserSrc struct {
	fakeUserSrc
	calls atomic.Int32
}

func (c *countingUserSrc) ServersForUser(ctx context.Context, u string) ([]Server, error) {
	c.calls.Add(1)
	return c.fakeUserSrc.ServersForUser(ctx, u)
}

// TestRegistry_SnapshotForResolvesUserServersOnce verifies a UserSnapshot
// resolves the user's DB-backed servers exactly once at SnapshotFor time,
// and that its ToolsFor/Call use that resolved slice — not a fresh
// ServersForUser query — however many times they're called afterward.
func TestRegistry_SnapshotForResolvesUserServersOnce(t *testing.T) {
	ts := newFakeGarminServer(t)
	userSrv := Server{Name: "mine", Scope: userScopePrefix + "alice", URL: ts.URL, Transport: transportStreamableHTTP}
	src := &countingUserSrc{fakeUserSrc: fakeUserSrc{perUser: map[string][]Server{"alice": {userSrv}}}}
	r := NewRegistry(nil, nil, src)

	snap := r.SnapshotFor(context.Background(), "alice")
	if got := src.calls.Load(); got != 1 {
		t.Fatalf("ServersForUser calls after SnapshotFor = %d, want 1", got)
	}

	for i := range 3 {
		if _, err := snap.ToolsFor(context.Background()); err != nil {
			t.Fatalf("ToolsFor iteration %d: %v", i, err)
		}
		if _, err := snap.Call(context.Background(), "mine__get_activities", `{}`); err != nil {
			t.Fatalf("Call iteration %d: %v", i, err)
		}
	}

	if got := src.calls.Load(); got != 1 {
		t.Fatalf("ServersForUser calls after 3 ToolsFor+Call rounds = %d, want still 1 (snapshot reused)", got)
	}
}

func TestRegistry_MergesUserServers(t *testing.T) {
	ts := newFakeGarminServer(t)
	userSrv := Server{Name: "mine", Scope: userScopePrefix + "alice", URL: ts.URL, Transport: transportStreamableHTTP}
	src := &fakeUserSrc{perUser: map[string][]Server{"alice": {userSrv}}}
	r := NewRegistry(nil, nil, src) // no env servers

	tools, err := r.ToolsFor(t.Context(), "alice")
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("alice got no tools from her user server")
	}
	if bt, _ := r.ToolsFor(t.Context(), "bob"); len(bt) != 0 {
		t.Fatalf("bob got %d tools, want 0", len(bt))
	}
	if len(r.Servers()) != 1 {
		t.Fatalf("Servers()=%d want 1", len(r.Servers()))
	}
}

// newHangingThenWorkingServer stands up a real MCP server (like
// newFakeGarminServer) but gates its very first HTTP request: the handler
// signals started (closed exactly once) and then blocks until release is
// closed, before serving the request normally. This lets tests observe "a
// dial is in flight but not yet complete" and then let it succeed on demand,
// exercising the real network round trip (not an interface fake).
func newHangingThenWorkingServer(t *testing.T) (ts *httptest.Server, started, release chan struct{}) {
	t.Helper()

	srv := mcpserver.NewMCPServer("fake-garmin", "0.0.1")
	tool := mcpgo.NewTool("get_activities", mcpgo.WithDescription("Get activities."))
	srv.AddTool(tool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(anonymizedActivitiesFixture), nil
	})
	httpSrv := mcpserver.NewStreamableHTTPServer(srv)

	started = make(chan struct{})
	release = make(chan struct{})
	var startOnce, gateOnce sync.Once
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateOnce.Do(func() {
			startOnce.Do(func() { close(started) })
			<-release
		})
		httpSrv.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		ts.Close()
	})
	return ts, started, release
}

// newFakeGarminServerCountingInitializes is like newFakeGarminServer but also
// counts how many "initialize" JSON-RPC requests actually reach the server —
// i.e. how many real dials were performed — so tests can assert concurrent
// callers for the same server shared a single dial instead of each dialing
// independently.
func newFakeGarminServerCountingInitializes(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	srv := mcpserver.NewMCPServer("fake-garmin", "0.0.1")
	tool := mcpgo.NewTool("get_activities", mcpgo.WithDescription("Get activities."))
	srv.AddTool(tool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(anonymizedActivitiesFixture), nil
	})
	httpSrv := mcpserver.NewStreamableHTTPServer(srv)

	var initializes atomic.Int32
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"initialize"`)) {
			initializes.Add(1)
		}
		httpSrv.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, &initializes
}

// TestRegistry_ClientForDialsOutsideLock_DifferentServersDontBlock verifies
// that clientFor's dial for one server never holds the registry lock, so a
// slow/hanging dial to one env server cannot block a concurrent clientFor
// call for a DIFFERENT server (e.g. a cache-hit lookup, or another server's
// own dial).
func TestRegistry_ClientForDialsOutsideLock_DifferentServersDontBlock(t *testing.T) {
	fastTs := newFakeGarminServer(t)
	slowTs, started, release := newHangingThenWorkingServer(t)

	slowServer := Server{Name: "slow", Scope: scopeGlobal, URL: slowTs.URL, Transport: transportStreamableHTTP}
	fastServer := Server{Name: testGarminName, Scope: scopeGlobal, URL: fastTs.URL, Transport: transportStreamableHTTP}
	reg := NewRegistry([]Server{slowServer, fastServer}, nil, nil)

	slowDone := make(chan error, 1)
	go func() {
		_, err := reg.clientFor(context.Background(), slowServer)
		slowDone <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow server's dial never reached the server")
	}

	// The slow dial is now blocked mid-handshake. A concurrent dial to a
	// DIFFERENT server must complete promptly rather than queueing behind it.
	fastDone := make(chan error, 1)
	go func() {
		_, err := reg.clientFor(context.Background(), fastServer)
		fastDone <- err
	}()

	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatalf("fast server's dial: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fast server's dial was blocked behind the slow server's in-flight dial")
	}

	close(release)
	if err := <-slowDone; err != nil {
		t.Fatalf("slow server's dial (after release): %v", err)
	}
}

// TestRegistry_ClientForDedupsConcurrentDialsToSameServer verifies that
// concurrent clientFor calls racing on a cache miss for the SAME server
// share a single in-flight dial: only one "initialize" handshake reaches the
// server, every caller gets the same client instance, and exactly one entry
// ends up cached.
func TestRegistry_ClientForDedupsConcurrentDialsToSameServer(t *testing.T) {
	ts, initializes := newFakeGarminServerCountingInitializes(t)
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP}
	reg := NewRegistry([]Server{s}, nil, nil)

	const n = 10
	var wg sync.WaitGroup
	clients := make([]mcpClient, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], errs[i] = reg.clientFor(context.Background(), s)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("clientFor[%d]: %v", i, err)
		}
	}
	for i, c := range clients {
		if c != clients[0] {
			t.Fatalf("clientFor[%d] returned a different client instance than [0]; want the one shared dial's result", i)
		}
	}
	if got := initializes.Load(); got != 1 {
		t.Fatalf("server saw %d initialize handshakes for %d concurrent dials to the same server, want exactly 1 (deduped)", got, n)
	}
	if n := len(reg.clients); n != 1 {
		t.Fatalf("clients cached = %d, want 1", n)
	}
}

// TestRegistry_EvictDuringInflightDialDoesNotPanicOrResurrectStale defines
// the eviction-during-inflight-dial behavior: evicting a server whose dial
// is still in flight is a harmless no-op (there is nothing cached yet to
// evict), it must not panic, and once the in-flight dial completes its
// result is cached normally — the earlier eviction must not "stick" and
// suppress caching a fresh, non-stale client.
func TestRegistry_EvictDuringInflightDialDoesNotPanicOrResurrectStale(t *testing.T) {
	ts, started, release := newHangingThenWorkingServer(t)
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP}
	reg := NewRegistry([]Server{s}, nil, nil)

	dialDone := make(chan error, 1)
	go func() {
		_, err := reg.clientFor(context.Background(), s)
		dialDone <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("dial never reached the server")
	}

	// Evict while the dial is still in flight: nothing is cached yet for
	// this key, so this must be a no-op — not a panic.
	reg.evictClient(s)
	if got := len(reg.clients); got != 0 {
		t.Fatalf("clients cached = %d, want 0 while the dial is still in flight", got)
	}

	close(release)
	if err := <-dialDone; err != nil {
		t.Fatalf("dial: %v", err)
	}

	// The dial's own (fresh, non-stale) result must land in the cache once
	// it completes; the earlier no-op eviction must not have suppressed it.
	if got := len(reg.clients); got != 1 {
		t.Fatalf("clients cached = %d, want 1 after the dial completes", got)
	}
}
