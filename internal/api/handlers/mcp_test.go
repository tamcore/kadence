package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/mcp"
	"github.com/tamcore/kadence/internal/model"
)

type fakeMcpHealth struct {
	status []mcp.ServerHealth
	tools  map[string][]mcp.ToolInfo
}

func (f *fakeMcpHealth) StatusFor(_ string) []mcp.ServerHealth {
	return f.status
}

func (f *fakeMcpHealth) ToolsFor(_, serverName string) ([]mcp.ToolInfo, bool) {
	t, ok := f.tools[serverName]
	return t, ok
}

// doAuthedGet builds an authed GET request for path, invokes fn, and returns
// the response body string.
func doAuthedGet(t *testing.T, fn http.HandlerFunc, role, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{Username: "u", Role: role}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Body.String()
}

type authedGetResult struct {
	Code int
	Body string
}

// doAuthedGetParam builds an authed GET request for path with a chi URL
// param attached, invokes fn, and returns the status code and body.
func doAuthedGetParam(t *testing.T, fn http.HandlerFunc, role, path, paramName, paramValue string) authedGetResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := auth.ContextWithUser(req.Context(), &model.User{Username: "u", Role: role})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(paramName, paramValue)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	fn(rec, req)
	return authedGetResult{Code: rec.Code, Body: rec.Body.String()}
}

func assertContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("expected body to contain %q, got: %s", w, body)
		}
	}
}

func assertNotContains(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(body, u) {
			t.Fatalf("expected body to NOT contain %q, got: %s", u, body)
		}
	}
}

func TestMCP_List_AdminSeesUrlAndError_MemberDoesNot(t *testing.T) {
	fake := &fakeMcpHealth{status: []mcp.ServerHealth{
		{Name: "garmin", Scope: "GLOBAL", Transport: "streamable-http", URL: "http://secret", OK: true, ToolCount: 3, CheckedAt: time.Now()},
		{Name: "down", Scope: "GLOBAL", URL: "http://d", OK: false, Err: "boom", CheckedAt: time.Now()},
	}}
	h := handlers.NewMCP(fake)

	adminBody := doAuthedGet(t, h.List, model.RoleAdmin, "/api/mcp")
	memberBody := doAuthedGet(t, h.List, model.RoleUser, "/api/mcp")

	assertContains(t, adminBody, `"url":"http://secret"`, `"error":"boom"`, `"state":"healthy"`, `"scope":"global"`)
	assertNotContains(t, memberBody, `http://secret`, `boom`)
	assertContains(t, memberBody, `"state":"unhealthy"`, `"toolCount":3`)
}

func TestMCP_Tools_404ForNonApplicable(t *testing.T) {
	fake := &fakeMcpHealth{tools: map[string][]mcp.ToolInfo{"garmin": {{Name: "get_x", Description: "d", Schema: []byte(`{"type":"object"}`)}}}}
	h := handlers.NewMCP(fake)

	ok := doAuthedGetParam(t, h.Tools, model.RoleUser, "/api/mcp/garmin/tools", "name", "garmin")
	if ok.Code != 200 {
		t.Fatalf("applicable server code=%d want 200", ok.Code)
	}
	assertContains(t, ok.Body, `"get_x"`, `"inputSchema"`)
	notfound := doAuthedGetParam(t, h.Tools, model.RoleUser, "/api/mcp/nope/tools", "name", "nope")
	if notfound.Code != 404 {
		t.Fatalf("non-applicable code=%d want 404", notfound.Code)
	}
}

func TestMCP_List_RequiresUser(t *testing.T) {
	h := handlers.NewMCP(&fakeMcpHealth{})
	req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestMCP_Tools_RequiresUser(t *testing.T) {
	h := handlers.NewMCP(&fakeMcpHealth{})
	req := httptest.NewRequest(http.MethodGet, "/api/mcp/garmin/tools", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "garmin")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Tools(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}
