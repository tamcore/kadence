package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	testGarminName       = "GARMIN"
	testUserPhilippScope = userScopePrefix + "philipp"
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
	})
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
	})

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
	reg := NewRegistry(nil)
	if reg.Enabled() {
		t.Fatal("registry with no servers must not be Enabled")
	}
}

func TestRegistry_CallUnknownTool(t *testing.T) {
	ts := newFakeGarminServer(t)
	reg := NewRegistry([]Server{
		{Name: testGarminName, Scope: scopeGlobal, URL: ts.URL, Transport: transportStreamableHTTP},
	})
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
			Tools:     []string{"get_*"},
		},
	})

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

func TestRegistry_CallRejectsFilteredOutTool(t *testing.T) {
	ts := newFakeGarminServerWithWorkouts(t)

	reg := NewRegistry([]Server{
		{
			Name:      testGarminName,
			Scope:     scopeGlobal,
			URL:       ts.URL,
			Transport: transportStreamableHTTP,
			Tools:     []string{"get_*"},
		},
	})

	ctx := context.Background()
	_, err := reg.Call(ctx, "anyuser", "garmin__create_run_workout", `{}`)
	if err == nil {
		t.Fatal("Call for a tool filtered out by Tools must error")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("want 'not enabled' error, got: %v", err)
	}
}
