package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
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
	testGarminAlias      = "garmin"
	testUserPhilippScope = userScopePrefix + "philipp"
	testGetToolsPattern  = "get_*"
	testCloakBrowserName = "CLOAKBROWSER"
	testBrowserAlias     = "browser"
	testEnvServerName    = "envone"
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

func TestUserSnapshotServerPrefixRespectsUserScope(t *testing.T) {
	reg := NewRegistry([]Server{
		{Name: "GARMIN1", Scope: "USER_alice", Alias: testGarminAlias},
		{Name: "GARMIN2", Scope: "USER_bob", Alias: testGarminAlias},
	}, nil, nil)

	alice := reg.SnapshotFor(t.Context(), "alice")
	if prefix, ok := alice.ServerPrefix("GARMIN1", "USER_alice"); !ok || prefix != testGarminAlias {
		t.Fatalf("alice prefix = %q, %v; want garmin, true", prefix, ok)
	}
	if prefix, ok := alice.ServerPrefix("GARMIN2", "USER_bob"); ok || prefix != "" {
		t.Fatalf("alice resolved bob route: prefix = %q, %v", prefix, ok)
	}

	bob := reg.SnapshotFor(t.Context(), "bob")
	if prefix, ok := bob.ServerPrefix("GARMIN2", "USER_bob"); !ok || prefix != testGarminAlias {
		t.Fatalf("bob prefix = %q, %v; want garmin, true", prefix, ok)
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
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP, FromEnv: true}
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

func TestRegistry_ToolsForUsesAliasAsPrefix(t *testing.T) {
	ts := newFakeGarminServer(t)
	reg := NewRegistry([]Server{
		{Name: testCloakBrowserName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP, Alias: testBrowserAlias},
	}, nil, nil)
	ctx := context.Background()

	tools, err := reg.ToolsFor(ctx, "anyuser")
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "browser__get_activities" {
		t.Fatalf("want browser__get_activities, got %+v", tools)
	}

	if _, err := reg.Call(ctx, "anyuser", "browser__get_activities", `{"limit":1}`); err != nil {
		t.Fatalf("Call via alias prefix: %v", err)
	}
	if _, err := reg.Call(ctx, "anyuser", "cloakbrowser__get_activities", `{}`); err == nil {
		t.Fatal("Call via the real name must fail once an alias is set (only the alias routes)")
	}
}

func TestRegistry_AliasCollisionFallsBackToName(t *testing.T) {
	ts1 := newFakeGarminServer(t)
	ts2 := newFakeGarminServer(t)
	reg := NewRegistry([]Server{
		{Name: testCloakBrowserName, Scope: scopeGlobal, URL: ts1.URL, Transport: transportStreamableHTTP, Alias: testBrowserAlias},
		{Name: "BROWSER", Scope: scopeGlobal, URL: ts2.URL, Transport: transportStreamableHTTP},
	}, nil, nil)
	ctx := context.Background()

	tools, err := reg.ToolsFor(ctx, "anyuser")
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("want 2 tools (one per server), got %d: %+v", len(tools), tools)
	}
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	// The real "browser" server reserves that prefix; the aliased server
	// must fall back to its own name instead of colliding.
	if !strings.Contains(strings.Join(names, ","), "cloakbrowser__get_activities") {
		t.Fatalf("want the alias to fall back to cloakbrowser__, got %v", names)
	}
	if !strings.Contains(strings.Join(names, ","), "browser__get_activities") {
		t.Fatalf("want the real browser server to keep browser__, got %v", names)
	}
}

func TestRegistry_ToolHints(t *testing.T) {
	ts := newFakeGarminServer(t)
	reg := NewRegistry([]Server{
		{Name: testCloakBrowserName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP,
			Alias: testBrowserAlias, Hint: "use for current info: weather, news, prices"},
		{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP},
	}, nil, nil)

	snap := reg.SnapshotFor(context.Background(), "anyuser")
	hints := snap.ToolHints()
	if len(hints) != 1 {
		t.Fatalf("want 1 hint line (only the hinted server), got %v", hints)
	}
	if hints[0] != "Tool guide: browser: use for current info: weather, news, prices" {
		t.Fatalf("hint line = %q", hints[0])
	}
}

func TestRegistry_ToolHintsEmptyWhenNoServerHasOne(t *testing.T) {
	ts := newFakeGarminServer(t)
	reg := NewRegistry([]Server{
		{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP},
	}, nil, nil)

	snap := reg.SnapshotFor(context.Background(), "anyuser")
	if hints := snap.ToolHints(); len(hints) != 0 {
		t.Fatalf("want no hint lines, got %v", hints)
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
		_, _, err := reg.clientFor(context.Background(), slowServer)
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
		_, _, err := reg.clientFor(context.Background(), fastServer)
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
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)

	const n = 10
	var wg sync.WaitGroup
	clients := make([]mcpClient, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], _, errs[i] = reg.clientFor(context.Background(), s)
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
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)

	dialDone := make(chan error, 1)
	go func() {
		_, _, err := reg.clientFor(context.Background(), s)
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

// TestRegistry_FollowerSurvivesLeaderContextCancellation verifies the fix for
// the shared-dial footgun: clientFor detaches the singleflight dial from
// whichever caller's context happened to start it, bounding it by
// dialTimeout instead. So when the "leader" caller (the one whose call
// actually invoked newClient) has its context canceled mid-dial — e.g. an
// aborted chat stream — a concurrent "follower" waiting on the same shared
// dial must still get a usable client once the server responds, rather than
// inheriting the leader's context.Canceled error.
func TestRegistry_FollowerSurvivesLeaderContextCancellation(t *testing.T) {
	ts, started, release := newHangingThenWorkingServer(t)
	s := Server{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())

	leaderDone := make(chan struct {
		c   mcpClient
		err error
	}, 1)
	go func() {
		c, _, err := reg.clientFor(leaderCtx, s)
		leaderDone <- struct {
			c   mcpClient
			err error
		}{c, err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("leader's dial never reached the server")
	}

	// A follower racing on the same key joins the in-flight dial.
	followerDone := make(chan error, 1)
	go func() {
		_, _, err := reg.clientFor(context.Background(), s)
		followerDone <- err
	}()

	// The leader aborts (e.g. its chat stream was canceled) while the dial
	// is still in flight, blocked on the server. Before the fix, this would
	// propagate context.Canceled to both the leader and the follower once
	// the dial's ctx.Done() fired.
	cancelLeader()

	// Give the canceled leader ctx a moment to have taken effect, if it
	// were (incorrectly) still wired into the dial.
	time.Sleep(50 * time.Millisecond)

	close(release)

	select {
	case res := <-leaderDone:
		if res.err != nil {
			t.Fatalf("leader clientFor: %v, want the detached dial to ignore the leader's canceled context", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("leader's clientFor never returned")
	}

	select {
	case err := <-followerDone:
		if err != nil {
			t.Fatalf("follower clientFor: %v, want the follower to get a usable client despite the leader's canceled context", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follower's clientFor never returned")
	}

	if n := len(reg.clients); n != 1 {
		t.Fatalf("clients cached = %d, want 1", n)
	}
}

// fakeCloseClient is the package's first mcpClient fake; it records Close calls.
type fakeCloseClient struct {
	closes atomic.Int32
	tools  []ToolInfo
}

func (f *fakeCloseClient) ListTools(context.Context) ([]ToolInfo, error) { return f.tools, nil }
func (f *fakeCloseClient) CallTool(context.Context, string, string) (string, error) {
	return "ok", nil
}
func (f *fakeCloseClient) Close() error {
	f.closes.Add(1)
	return nil
}

func TestRegistryNeverCachesUserDefinedClient(t *testing.T) {
	ts := newFakeGarminServer(t)
	userSrv := Server{
		Name: "userone", Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP,
	}
	// No env servers, so userSrv is a user-defined server.
	reg := NewRegistry(nil, nil, &fakeUserSrc{perUser: map[string][]Server{"owner": {userSrv}}})

	if _, err := reg.ToolsFor(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("user-defined clients must never be cached; cache size = %d", n)
	}
}

func TestRegistryCloseClosesEveryCachedClient(t *testing.T) {
	reg := NewRegistry(nil, nil, nil)
	a := &fakeCloseClient{}
	b := &fakeCloseClient{}
	reg.mu.Lock()
	reg.clients["a/global"] = &leasedClient{client: a}
	reg.clients["b/global"] = &leasedClient{client: b}
	reg.mu.Unlock()

	if err := reg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := a.closes.Load(); got != 1 {
		t.Fatalf("client a closed %d times, want 1", got)
	}
	if got := b.closes.Load(); got != 1 {
		t.Fatalf("client b closed %d times, want 1", got)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("cache not cleared after Close; size = %d", n)
	}
	// Close must be idempotent.
	if err := reg.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := a.closes.Load(); got != 1 {
		t.Fatalf("client a closed %d times after a second Close, want 1", got)
	}
}

func TestEvictClientClosesTheDroppedClient(t *testing.T) {
	s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)
	c := &fakeCloseClient{}
	reg.mu.Lock()
	reg.clients[testEnvServerName+"/"+scopeGlobal] = &leasedClient{client: c}
	reg.mu.Unlock()

	reg.evictClient(s)

	if got := c.closes.Load(); got != 1 {
		t.Fatalf("evicted client closed %d times, want 1", got)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("client still cached after eviction; size = %d", n)
	}
}

func TestClientForReleaseIsNoOpForCachedEnvClient(t *testing.T) {
	s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)
	c := &fakeCloseClient{}
	reg.mu.Lock()
	reg.clients[testEnvServerName+"/"+scopeGlobal] = &leasedClient{client: c}
	reg.mu.Unlock()

	got, release, err := reg.clientFor(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Fatal("clientFor did not return the cached client")
	}
	release()
	if n := c.closes.Load(); n != 0 {
		t.Fatalf("release closed a cached env client %d times, want 0", n)
	}
}

func TestToolsForClosesEachUserDefinedClientPerIteration(t *testing.T) {
	var mu sync.Mutex
	var created []*fakeCloseClient
	firstClosedBeforeSecondDial := false

	restore := dialClient
	dialClient = func(_ context.Context, s Server, _ *http.Client) (mcpClient, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(created) == 1 {
			// The loop is about to dial the second server; the first must
			// already have been released.
			firstClosedBeforeSecondDial = created[0].closes.Load() == 1
		}
		c := &fakeCloseClient{tools: []ToolInfo{{Name: "tool_" + s.Name}}}
		created = append(created, c)
		return c, nil
	}
	t.Cleanup(func() { dialClient = restore })

	reg := NewRegistry(nil, nil, &fakeUserSrc{perUser: map[string][]Server{
		"owner": {
			{Name: "u1", Scope: scopeGlobal, URL: "https://u1.invalid", Transport: transportStreamableHTTP},
			{Name: "u2", Scope: scopeGlobal, URL: "https://u2.invalid", Transport: transportStreamableHTTP},
		},
	}})

	defs, err := reg.ToolsFor(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("tool defs = %d, want 2 (one per user-defined server)", len(defs))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(created) != 2 {
		t.Fatalf("dialed %d clients, want 2", len(created))
	}
	if !firstClosedBeforeSecondDial {
		t.Fatal("first user-defined client was still open when the second was dialed — release must fire per iteration, not at function return")
	}
	for i, c := range created {
		if n := c.closes.Load(); n != 1 {
			t.Fatalf("client %d closed %d times, want exactly 1", i, n)
		}
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("user-defined clients must never be cached; cache size = %d", n)
	}
}

// TestEvictClientDoesNotCloseALeasedClient proves the fix for the regression
// found by adversarial review: evicting a cached env client that is still
// leased by an in-flight caller must not close it out from under that
// caller. It must, however, remove it from the cache so the next clientFor
// dials afresh instead of resurrecting the (now-stale) evicted entry.
func TestEvictClientDoesNotCloseALeasedClient(t *testing.T) {
	s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)
	c := &fakeCloseClient{}
	key := testEnvServerName + "/" + scopeGlobal
	reg.mu.Lock()
	reg.clients[key] = &leasedClient{client: c, leases: 1}
	reg.mu.Unlock()

	reg.evictClient(s)

	if got := c.closes.Load(); got != 0 {
		t.Fatalf("leased client closed %d times during eviction, want 0", got)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("evicted client still cached; size = %d", n)
	}

	restore := dialClient
	fresh := &fakeCloseClient{}
	dialClient = func(context.Context, Server, *http.Client) (mcpClient, error) { return fresh, nil }
	t.Cleanup(func() { dialClient = restore })

	got, release, err := reg.clientFor(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if got != fresh {
		t.Fatal("clientFor did not dial afresh after eviction of a still-leased client")
	}
	release()
}

// TestReleaseAfterEvictionClosesExactlyOnce verifies that once an evicted
// client's last outstanding lease is released, its transport is closed
// exactly once.
func TestReleaseAfterEvictionClosesExactlyOnce(t *testing.T) {
	s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)
	c := &fakeCloseClient{}
	lc := &leasedClient{client: c, leases: 1}
	key := testEnvServerName + "/" + scopeGlobal
	reg.mu.Lock()
	reg.clients[key] = lc
	reg.mu.Unlock()

	reg.evictClient(s)
	if got := c.closes.Load(); got != 0 {
		t.Fatalf("client closed %d times immediately after eviction, want 0 (still leased)", got)
	}

	reg.release(lc)
	if got := c.closes.Load(); got != 1 {
		t.Fatalf("client closed %d times after releasing the last lease, want exactly 1", got)
	}
}

// TestCloseDefersLeasedClientUntilRelease verifies Registry.Close closes
// unleased cached clients immediately, but a client with an outstanding
// lease is only closed once that lease is released.
func TestCloseDefersLeasedClientUntilRelease(t *testing.T) {
	reg := NewRegistry(nil, nil, nil)
	leased := &fakeCloseClient{}
	free := &fakeCloseClient{}
	leasedLC := &leasedClient{client: leased, leases: 1}
	reg.mu.Lock()
	reg.clients["leased/global"] = leasedLC
	reg.clients["free/global"] = &leasedClient{client: free}
	reg.mu.Unlock()

	if err := reg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := free.closes.Load(); got != 1 {
		t.Fatalf("unleased client closed %d times by Close, want 1", got)
	}
	if got := leased.closes.Load(); got != 0 {
		t.Fatalf("leased client closed %d times by Close while still leased, want 0", got)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("cache not cleared after Close; size = %d", n)
	}

	reg.release(leasedLC)
	if got := leased.closes.Load(); got != 1 {
		t.Fatalf("leased client closed %d times after its last release, want exactly 1", got)
	}
}

// TestTwoLeasesOnSameClientOnlyLastReleaseCloses verifies that when two
// callers concurrently hold the same cached (now-evicted) client, the first
// release closes nothing and the second closes it exactly once.
func TestTwoLeasesOnSameClientOnlyLastReleaseCloses(t *testing.T) {
	s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)
	c := &fakeCloseClient{}
	key := testEnvServerName + "/" + scopeGlobal
	reg.mu.Lock()
	reg.clients[key] = &leasedClient{client: c}
	reg.mu.Unlock()

	_, release1, ok1 := reg.leaseCached(key)
	if !ok1 {
		t.Fatal("expected a cache hit for the first lease")
	}
	_, release2, ok2 := reg.leaseCached(key)
	if !ok2 {
		t.Fatal("expected a cache hit for the second lease")
	}

	reg.evictClient(s) // mark evicted while both leases are still outstanding

	release1()
	if got := c.closes.Load(); got != 0 {
		t.Fatalf("client closed after the first release with a lease still outstanding, want 0, got %d", got)
	}
	release2()
	if got := c.closes.Load(); got != 1 {
		t.Fatalf("client closed %d times after the last release, want exactly 1", got)
	}
}

// TestClientForTreatsCollidingUserServerAsUserDefinedNotEnv proves provenance
// is read from Server.FromEnv rather than inferred by matching Name+Scope
// against the registry's env set. A user-defined (DB) server can legitimately
// collide with an env server on both fields; before this fix, isEnvServer's
// name-matching would misclassify it as env-configured, letting it (or its
// credentials) enter the shared env cache. The colliding user-defined server
// must still dial fresh and close on release, while the identically
// Name+Scope'd env server continues to cache independently.
func TestClientForTreatsCollidingUserServerAsUserDefinedNotEnv(t *testing.T) {
	envClient := &fakeCloseClient{}
	userClient := &fakeCloseClient{}

	restore := dialClient
	dialClient = func(_ context.Context, s Server, _ *http.Client) (mcpClient, error) {
		if s.FromEnv {
			return envClient, nil
		}
		return userClient, nil
	}
	t.Cleanup(func() { dialClient = restore })

	envSrv := Server{
		Name: "collide", Scope: scopeGlobal, URL: "https://collide.invalid",
		Transport: transportStreamableHTTP, FromEnv: true,
	}
	reg := NewRegistry([]Server{envSrv}, nil, nil)

	// A user-defined server colliding with the env server on BOTH Name and
	// Scope, with FromEnv left at its zero value — exactly what any real
	// UserServerSource returns.
	userSrv := Server{
		Name: "collide", Scope: scopeGlobal, URL: "https://collide.invalid",
		Transport: transportStreamableHTTP,
	}

	got, release, err := reg.clientFor(context.Background(), userSrv)
	if err != nil {
		t.Fatal(err)
	}
	if got != userClient {
		t.Fatal("clientFor dialed the wrong client for a colliding user-defined server")
	}
	release()
	if got := userClient.closes.Load(); got != 1 {
		t.Fatalf("user-defined client closed %d times, want 1 (closed on release)", got)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("colliding user-defined server entered the cache; size = %d", n)
	}

	got2, release2, err := reg.clientFor(context.Background(), envSrv)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != envClient {
		t.Fatal("clientFor dialed the wrong client for the env server")
	}
	release2()
	if got := envClient.closes.Load(); got != 0 {
		t.Fatalf("env client closed %d times after release, want 0 (cache owns its lifetime)", got)
	}
	if n := len(reg.clients); n != 1 {
		t.Fatalf("env server did not cache independently; cache size = %d", n)
	}
}

// fanOutEvictionRound is one reproduction of the singleflight fan-out eviction
// interleaving, handed to the assert callback of runFanOutEvictionRounds. The
// callback owns releaseFollower and must call it.
type fanOutEvictionRound struct {
	reg *Registry
	// sharedClient is the product of the one blocked dial the leader owns and
	// the follower joins. By the time assert runs it is already closed: the
	// leader evicted it (leases still 1, nothing closes) then released it
	// (leases -> 0, transport closed).
	sharedClient *fakeCloseClient
	// followerClient is what the follower's clientFor handed back.
	followerClient  mcpClient
	releaseFollower func()
	// freshClients are the clients dialed by anyone other than the leader —
	// i.e. the uncached replacement clientFor dials when it finds the shared
	// record already closed.
	freshClients []*fakeCloseClient
}

// runFanOutEvictionRounds drives the ACTUAL singleflight fan-out race instead
// of injecting lease records by hand (which is why the earlier lease tests
// missed the defects here): two concurrent clientFor calls share one dial, the
// leader returns, evicts and releases — closing the shared client — and only
// then does the follower resume into its post-dial branch.
//
// GOMAXPROCS(1) plus channel handoffs pin the interleaving: the follower only
// becomes runnable when the leader's dial returns, and the leader then runs its
// insert/evict/release without a yield point.
//
// Rounds where the follower never joined the fan-out are skipped as
// non-reproductions. The discriminator is that a follower which DID join can
// only dial afterwards (the fresh replacement dial), i.e. once the leader has
// finished, whereas a follower that missed the in-flight dial dials while the
// leader is still blocked on it. The helper fails if no round reproduced.
func runFanOutEvictionRounds(t *testing.T, assert func(round int, r fanOutEvictionRound)) {
	t.Helper()

	prev := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })

	restore := dialClient
	t.Cleanup(func() { dialClient = restore })

	const rounds = 50
	reproduced := 0
	for round := range rounds {
		s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
		reg := NewRegistry([]Server{s}, nil, nil)

		sharedClient := &fakeCloseClient{}
		var dials atomic.Int32
		var leaderFinished, dialedAfterLeaderFinished atomic.Bool
		var freshMu sync.Mutex
		var fresh []*fakeCloseClient
		dialStarted := make(chan struct{})
		releaseDial := make(chan struct{})
		dialClient = func(context.Context, Server, *http.Client) (mcpClient, error) {
			if dials.Add(1) == 1 {
				dialStarted <- struct{}{}
				<-releaseDial
				return sharedClient, nil
			}
			if leaderFinished.Load() {
				dialedAfterLeaderFinished.Store(true)
			}
			c := &fakeCloseClient{}
			freshMu.Lock()
			fresh = append(fresh, c)
			freshMu.Unlock()
			return c, nil
		}

		// Leader: dials, then immediately evicts and drops its lease — the
		// eviction path every failing ListTools/CallTool takes.
		type result struct {
			c   mcpClient
			err error
		}
		leaderDone := make(chan result, 1)
		go func() {
			c, release, err := reg.clientFor(context.Background(), s)
			if err == nil {
				reg.evictClient(s)
				release()
			}
			leaderFinished.Store(true)
			leaderDone <- result{c, err}
		}()
		<-dialStarted

		followerReady := make(chan struct{})
		followerDone := make(chan struct {
			result
			release func()
		}, 1)
		go func() {
			followerReady <- struct{}{}
			c, release, err := reg.clientFor(context.Background(), s)
			followerDone <- struct {
				result
				release func()
			}{result{c, err}, release}
		}()
		<-followerReady

		close(releaseDial)
		leader := <-leaderDone
		follower := <-followerDone

		if leader.err != nil {
			t.Fatalf("round %d: leader clientFor: %v", round, leader.err)
		}
		if follower.err != nil {
			t.Fatalf("round %d: follower clientFor: %v", round, follower.err)
		}
		if dials.Load() != 1 && !dialedAfterLeaderFinished.Load() {
			follower.release()
			continue
		}
		if n := sharedClient.closes.Load(); n != 1 {
			t.Fatalf("round %d: leader's evict+release closed the shared client %d times, want 1", round, n)
		}
		reproduced++

		freshMu.Lock()
		freshCopy := append([]*fakeCloseClient(nil), fresh...)
		freshMu.Unlock()
		assert(round, fanOutEvictionRound{
			reg:             reg,
			sharedClient:    sharedClient,
			followerClient:  follower.c,
			releaseFollower: follower.release,
			freshClients:    freshCopy,
		})
	}

	if reproduced == 0 {
		t.Fatal("no round observed two callers sharing one dial; the race was never exercised")
	}
}

// TestRegistry_FanOutWaiterDoesNotCacheAnEvictedClient asserts the fan-out
// eviction interleaving leaves the cache empty.
//
// With the lease record created per-waiter after the dial, the follower found
// an empty map and cached a SECOND record wrapping the now-closed client, so
// every later cache hit got a dead transport. With the record created inside
// the singleflight, both callers share one record: nothing is re-cached after
// the eviction, and the client is closed exactly once.
func TestRegistry_FanOutWaiterDoesNotCacheAnEvictedClient(t *testing.T) {
	runFanOutEvictionRounds(t, func(round int, r fanOutEvictionRound) {
		if n := len(r.reg.clients); n != 0 {
			t.Fatalf("round %d: cache holds %d record(s) for a client the leader already evicted and closed; a later cache hit would get a dead transport", round, n)
		}
		r.releaseFollower()
		if got := r.sharedClient.closes.Load(); got != 1 {
			t.Fatalf("round %d: shared client closed %d times, want exactly 1", round, got)
		}
	})
}

// TestRegistry_DialCompletingAfterCloseIsNotCached covers the shutdown edge of
// the same window: a dial that lands after Registry.Close must not re-populate
// the cache (nothing would ever close it), and its release must close it.
func TestRegistry_DialCompletingAfterCloseIsNotCached(t *testing.T) {
	s := Server{Name: testEnvServerName, Scope: scopeGlobal, FromEnv: true}
	reg := NewRegistry([]Server{s}, nil, nil)

	client := &fakeCloseClient{}
	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	restore := dialClient
	dialClient = func(context.Context, Server, *http.Client) (mcpClient, error) {
		dialStarted <- struct{}{}
		<-releaseDial
		return client, nil
	}
	t.Cleanup(func() { dialClient = restore })

	type result struct {
		release func()
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, release, err := reg.clientFor(context.Background(), s)
		done <- result{release, err}
	}()
	<-dialStarted

	if err := reg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(releaseDial)

	got := <-done
	if got.err != nil {
		t.Fatalf("clientFor: %v", got.err)
	}
	if n := len(reg.clients); n != 0 {
		t.Fatalf("a dial completing after Close cached %d client(s); nothing would ever close them", n)
	}
	if n := client.closes.Load(); n != 0 {
		t.Fatalf("client closed %d times while still leased by its caller, want 0", n)
	}
	got.release()
	if n := client.closes.Load(); n != 1 {
		t.Fatalf("client closed %d times after its last release, want exactly 1", n)
	}
}

// TestRegistry_FanOutWaiterNeverReceivesAClosedClient asserts the stronger
// property over the same interleaving: a waiter whose peer already evicted AND
// fully released the shared client must not be handed that dead transport. This
// is preventable, not a residual — the shared lease record carries `closed`, so
// the waiter can detect it and dial a fresh, uncached client instead.
func TestRegistry_FanOutWaiterNeverReceivesAClosedClient(t *testing.T) {
	runFanOutEvictionRounds(t, func(round int, r fanOutEvictionRound) {
		fc, ok := r.followerClient.(*fakeCloseClient)
		if !ok {
			t.Fatalf("round %d: follower got %T, want *fakeCloseClient", round, r.followerClient)
		}
		if fc.closes.Load() != 0 {
			t.Fatalf("round %d: follower was handed an ALREADY-CLOSED client; its call would fail against a dead transport", round)
		}

		r.reg.mu.Lock()
		for key, lc := range r.reg.clients {
			dead := lc.closed
			if cc, isFake := lc.client.(*fakeCloseClient); isFake && cc.closes.Load() != 0 {
				dead = true
			}
			if dead {
				r.reg.mu.Unlock()
				t.Fatalf("round %d: cache key %q holds a record referencing a closed client", round, key)
			}
		}
		r.reg.mu.Unlock()

		r.releaseFollower()
		if n := r.sharedClient.closes.Load(); n != 1 {
			t.Fatalf("round %d: shared client closed %d times, want exactly 1", round, n)
		}
		for i, c := range r.freshClients {
			if n := c.closes.Load(); n != 1 {
				t.Fatalf("round %d: fresh client %d closed %d times after release, want exactly 1", round, i, n)
			}
		}
	})
}
