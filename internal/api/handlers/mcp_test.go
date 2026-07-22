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
	"github.com/tamcore/kadence/internal/store"
)

const allowedTestHost = "a.example.io"

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

// fakeUserStore is a test double for the mcpUserStore seam.
type fakeUserStore struct {
	created   bool
	createErr error
	deleteErr error
	updated   bool
	updateErr error
	listRecs  []store.UserMCPRecord
	listErr   error
}

func (f *fakeUserStore) Create(_ context.Context, _ int64, _ store.UserMCPInput) (int64, error) {
	f.created = true
	if f.createErr != nil {
		return 0, f.createErr
	}
	return 1, nil
}

func (f *fakeUserStore) Update(_ context.Context, _, _ int64, _ store.UserMCPInput) error {
	f.updated = true
	return f.updateErr
}

func (f *fakeUserStore) Delete(_ context.Context, _, _ int64) error {
	return f.deleteErr
}

func (f *fakeUserStore) ListForOwner(_ context.Context, _ int64) ([]store.UserMCPRecord, error) {
	return f.listRecs, f.listErr
}

// doAuthedPost builds a RoleUser-authed POST request with a JSON body,
// invokes fn, and returns the status code.
func doAuthedPost(t *testing.T, fn http.HandlerFunc, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u", Role: model.RoleUser}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Code
}

// doAuthedPostBody builds an authed POST request with a JSON body, invokes
// fn, and returns the response body string.
func doAuthedPostBody(t *testing.T, fn http.HandlerFunc, role, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u", Role: role}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Body.String()
}

// doAuthedDeleteParam builds an authed DELETE request with a chi URL param
// attached, invokes fn, and returns the status code.
func doAuthedDeleteParam(t *testing.T, fn http.HandlerFunc, role, paramName, paramValue string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/mcp/"+paramValue, nil)
	ctx := auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u", Role: role})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(paramName, paramValue)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Code
}

// doAuthedPutParam builds an authed (RoleUser) PUT request with a JSON body
// and a chi "id" URL param attached, invokes fn, and returns the status code
// and body.
func doAuthedPutParam(t *testing.T, fn http.HandlerFunc, paramValue, body string) authedGetResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/mcp/"+paramValue, strings.NewReader(body))
	ctx := auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u", Role: model.RoleUser})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", paramValue)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	fn(rec, req)
	return authedGetResult{Code: rec.Code, Body: rec.Body.String()}
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
	h := handlers.NewMCP(fake, nil, nil, false, 10)

	adminBody := doAuthedGet(t, h.List, model.RoleAdmin, "/api/mcp")
	memberBody := doAuthedGet(t, h.List, model.RoleUser, "/api/mcp")

	assertContains(t, adminBody, `"url":"http://secret"`, `"error":"boom"`, `"state":"healthy"`, `"scope":"global"`)
	assertNotContains(t, memberBody, `http://secret`, `boom`)
	assertContains(t, memberBody, `"state":"unhealthy"`, `"toolCount":3`)
}

func TestMCP_Tools_404ForNonApplicable(t *testing.T) {
	fake := &fakeMcpHealth{tools: map[string][]mcp.ToolInfo{"garmin": {{Name: "get_x", Description: "d", Schema: []byte(`{"type":"object"}`)}}}}
	h := handlers.NewMCP(fake, nil, nil, false, 10)

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

func TestMCP_Create_AllowlistAndDisabled(t *testing.T) {
	us := &fakeUserStore{}
	hDisabled := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, false, 10)
	if code := doAuthedPost(t, hDisabled.Create, `{"name":"x","url":"https://`+allowedTestHost+`/mcp","transport":"sse"}`); code != 403 {
		t.Fatalf("disabled create code=%d want 403", code)
	}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	if code := doAuthedPost(t, h.Create, `{"name":"x","url":"https://evil.com/mcp","transport":"sse"}`); code != 400 {
		t.Fatalf("bad-host create code=%d want 400", code)
	}
	body := doAuthedPostBody(t, h.Create, model.RoleUser, `{"name":"x","url":"https://`+allowedTestHost+`/mcp","transport":"sse","authUser":"u","authPass":"p"}`)
	if !us.created {
		t.Fatal("store.Create not called")
	}
	if strings.Contains(body, `authPass`) || strings.Contains(body, `"p"`) {
		t.Fatalf("create response leaked password: %s", body)
	}
}

func TestMCP_Create_RejectsInvalidTransport(t *testing.T) {
	us := &fakeUserStore{}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	body := `{"name":"x","url":"https://` + allowedTestHost + `/mcp","transport":"websocket"}`
	if code := doAuthedPost(t, h.Create, body); code != 400 {
		t.Fatalf("bad-transport create code=%d want 400", code)
	}
	if us.created {
		t.Fatal("store.Create should not be called for an invalid transport")
	}
}

func TestMCP_Create_RejectsInvalidName(t *testing.T) {
	us := &fakeUserStore{}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	for _, name := range []string{"Garmin", "my_server", "-leading", "a__b", ""} {
		body := `{"name":"` + name + `","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
		if code := doAuthedPost(t, h.Create, body); code != 400 {
			t.Fatalf("name %q create code=%d want 400", name, code)
		}
	}
	if us.created {
		t.Fatal("store.Create should not be called for an invalid name")
	}
}

func TestMCP_Create_RejectsOverCap(t *testing.T) {
	us := &fakeUserStore{listRecs: []store.UserMCPRecord{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 2)
	body := `{"name":"c","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	if code := doAuthedPost(t, h.Create, body); code != 400 {
		t.Fatalf("over-cap create code=%d want 400", code)
	}
	if us.created {
		t.Fatal("store.Create should not be called once the owner is at the cap")
	}
}

func TestMCP_Create_AllowsUnderCap(t *testing.T) {
	us := &fakeUserStore{listRecs: []store.UserMCPRecord{{ID: 1, Name: "a"}}}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 2)
	body := `{"name":"c","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	if code := doAuthedPost(t, h.Create, body); code != 201 {
		t.Fatalf("under-cap create code=%d want 201", code)
	}
	if !us.created {
		t.Fatal("store.Create should be called when under the cap")
	}
}

func TestMCP_Update_RejectsInvalidTransportAndName(t *testing.T) {
	us := &fakeUserStore{}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)

	badTransport := `{"name":"x","url":"https://` + allowedTestHost + `/mcp","transport":"ws"}`
	if res := doAuthedPutParam(t, h.Update, "1", badTransport); res.Code != 400 {
		t.Fatalf("bad-transport update code=%d want 400", res.Code)
	}

	badName := `{"name":"a__b","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	if res := doAuthedPutParam(t, h.Update, "1", badName); res.Code != 400 {
		t.Fatalf("bad-name update code=%d want 400", res.Code)
	}
	if us.updated {
		t.Fatal("store.Update should not be called for invalid transport/name")
	}
}

func TestMCP_Update_GrandfathersLegacyNameWhenUnchanged(t *testing.T) {
	// A server created before name-format validation existed may have a
	// legacy name (e.g. uppercase) that would now fail ValidateServerName.
	// Resubmitting the update with that same name (the frontend's normal
	// "unchanged fields" flow) must not 400.
	us := &fakeUserStore{listRecs: []store.UserMCPRecord{{ID: 1, Name: "Legacy_Name"}}}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)

	body := `{"name":"Legacy_Name","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	res := doAuthedPutParam(t, h.Update, "1", body)
	if res.Code != 200 {
		t.Fatalf("grandfathered legacy-name update code=%d want 200, body=%s", res.Code, res.Body)
	}
	if !us.updated {
		t.Fatal("store.Update should be called when the legacy name is unchanged")
	}
}

func TestMCP_Update_RenamingLegacyNameStillValidatesFormat(t *testing.T) {
	// Renaming a legacy server to a *different* but still-invalid name must
	// still 400 — grandfathering only covers the unchanged-name case.
	us := &fakeUserStore{listRecs: []store.UserMCPRecord{{ID: 1, Name: "Legacy_Name"}}}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)

	body := `{"name":"Still_Invalid","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	res := doAuthedPutParam(t, h.Update, "1", body)
	if res.Code != 400 {
		t.Fatalf("rename to another invalid name code=%d want 400, body=%s", res.Code, res.Body)
	}
	if us.updated {
		t.Fatal("store.Update should not be called for a rename to an invalid name")
	}
}

func TestMCP_Update_DisabledForbidden(t *testing.T) {
	us := &fakeUserStore{}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, false, 10)
	body := `{"name":"x","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	res := doAuthedPutParam(t, h.Update, "1", body)
	if res.Code != 403 {
		t.Fatalf("disabled update code=%d want 403", res.Code)
	}
	if us.updated {
		t.Fatal("store.Update should not be called when feature disabled")
	}
}

func TestMCP_Update_BadHostRejected(t *testing.T) {
	us := &fakeUserStore{}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	body := `{"name":"x","url":"https://evil.com/mcp","transport":"sse"}`
	res := doAuthedPutParam(t, h.Update, "1", body)
	if res.Code != 400 {
		t.Fatalf("bad-host update code=%d want 400", res.Code)
	}
	if us.updated {
		t.Fatal("store.Update should not be called when host is not allowlisted")
	}
}

func TestMCP_Update_NotFound(t *testing.T) {
	us := &fakeUserStore{updateErr: store.ErrNotFound}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	body := `{"name":"x","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	res := doAuthedPutParam(t, h.Update, "9", body)
	if res.Code != 404 {
		t.Fatalf("not-found update code=%d want 404", res.Code)
	}
}

func TestMCP_Update_DuplicateName(t *testing.T) {
	us := &fakeUserStore{updateErr: store.ErrDuplicateName}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	body := `{"name":"dup","url":"https://` + allowedTestHost + `/mcp","transport":"sse"}`
	res := doAuthedPutParam(t, h.Update, "1", body)
	if res.Code != 409 {
		t.Fatalf("duplicate-name update code=%d want 409", res.Code)
	}
}

func TestMCP_Update_Success_NoPasswordLeak(t *testing.T) {
	us := &fakeUserStore{}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	body := `{"name":"x","url":"https://` + allowedTestHost + `/mcp","transport":"sse","authUser":"u","authPass":"secretpw"}`
	res := doAuthedPutParam(t, h.Update, "1", body)
	if res.Code != 200 {
		t.Fatalf("success update code=%d want 200", res.Code)
	}
	if !us.updated {
		t.Fatal("store.Update not called")
	}
	assertNotContains(t, res.Body, "secretpw", "authPass")
}

func TestMCP_DeleteOwnerScoped(t *testing.T) {
	us := &fakeUserStore{deleteErr: store.ErrNotFound}
	h := handlers.NewMCP(&fakeMcpHealth{}, us, []string{allowedTestHost}, true, 10)
	if code := doAuthedDeleteParam(t, h.Delete, model.RoleUser, "id", "9"); code != 404 {
		t.Fatalf("delete non-owned code=%d want 404", code)
	}
}
