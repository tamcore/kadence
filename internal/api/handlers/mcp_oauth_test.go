package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/mcp/oauth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const (
	oauthTestServerID = "garmin"
	testReadScope     = "garmin:read"
	oauthTestAuthURL  = "https://garmin.example.invalid/authorize?state=s"
	oauthTestCode     = "code-should-not-leak"
	oauthTestState    = "state-should-not-leak"
	oauthTestBrowserT = "browser-token-should-not-leak"
	oauthCallbackBase = "/api/mcp/oauth/callback"
	oauthStartTarget  = "/api/mcp/oauth/garmin/start"
)

// fakeLinkService records what it was asked and answers with canned results.
type fakeLinkService struct {
	startURL    string
	startState  string
	startToken  string
	startErr    error
	completeErr error
	completed   int
	unlinked    int
	states      []oauth.LinkState
}

func (f *fakeLinkService) Start(context.Context, int64, string) (string, string, string, error) {
	if f.startErr != nil {
		return "", "", "", f.startErr
	}
	return f.startURL, f.startState, f.startToken, nil
}

func (f *fakeLinkService) Complete(_ context.Context, _ int64, _, _, _ string) (string, error) {
	f.completed++
	if f.completeErr != nil {
		return "", f.completeErr
	}
	return oauthTestServerID, nil
}

func (f *fakeLinkService) Unlink(context.Context, int64, string) error {
	f.unlinked++
	return nil
}

func (f *fakeLinkService) Integrations(context.Context, int64) ([]oauth.LinkState, error) {
	return f.states, nil
}

// serveOAuth routes one request through the handler with an authenticated user
// in the context, mirroring what LoadUser + RequireAuth put there.
func serveOAuth(t *testing.T, h *handlers.MCPOAuth, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/api/mcp/oauth/{server}/start", h.Start)
	r.Get(oauthCallbackBase, h.Callback)
	r.Delete("/api/mcp/oauth/{server}", h.Unlink)
	r.Get("/api/mcp/integrations", h.List)

	req := httptest.NewRequest(method, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	ctx := auth.ContextWithUser(req.Context(), &model.User{ID: 42, Username: "alice"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func newOAuthHandler(svc *fakeLinkService) *handlers.MCPOAuth {
	return handlers.NewMCPOAuth(svc, false)
}

func bindingCookie(state string) *http.Cookie {
	return &http.Cookie{
		Name:  handlers.MCPOAuthCookieName(false),
		Value: state + "." + oauthTestBrowserT,
	}
}

func TestStartReturnsTheAuthorizeURLAndSetsTheBindingCookie(t *testing.T) {
	svc := &fakeLinkService{startURL: oauthTestAuthURL, startState: oauthTestState, startToken: oauthTestBrowserT}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodPost, oauthStartTarget, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			AuthorizeURL string `json:"authorize_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.AuthorizeURL != oauthTestAuthURL {
		t.Fatalf("authorize_url = %q", body.Data.AuthorizeURL)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if !strings.Contains(c.Value, oauthTestBrowserT) || !strings.Contains(c.Value, oauthTestState) {
		t.Fatal("the cookie does not bind the state to the browser token")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie attributes are wrong: %+v", c)
	}
	if c.MaxAge <= 0 {
		t.Fatalf("cookie has no expiry: %+v", c)
	}
}

func TestStartRefusesTooManyAttempts(t *testing.T) {
	svc := &fakeLinkService{startErr: oauth.ErrTooManyAttempts}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodPost, oauthStartTarget, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

func TestStartRefusesAnUnknownServer(t *testing.T) {
	svc := &fakeLinkService{startErr: oauth.ErrUnknownServer}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodPost, "/api/mcp/oauth/nope/start", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestStartSurfacesAnUnexpectedFailureWithoutEchoingIt(t *testing.T) {
	svc := &fakeLinkService{startErr: errors.New("boom")}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodPost, oauthStartTarget, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("the response echoed the internal error: %s", rec.Body.String())
	}
}

func TestCallbackLinksAndRedirects(t *testing.T) {
	svc := &fakeLinkService{}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet,
		oauthCallbackBase+"?code="+oauthTestCode+"&state="+oauthTestState,
		bindingCookie(oauthTestState))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/profile?integration=garmin&status=linked" {
		t.Fatalf("Location = %q", got)
	}
	if svc.completed != 1 {
		t.Fatalf("Complete called %d times, want 1", svc.completed)
	}
}

func TestCallbackNeverLeaksTheCodeOrState(t *testing.T) {
	svc := &fakeLinkService{}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet,
		oauthCallbackBase+"?code="+oauthTestCode+"&state="+oauthTestState,
		bindingCookie(oauthTestState))

	haystack := rec.Header().Get("Location") + "\n" + rec.Body.String()
	for _, secret := range []string{oauthTestCode, oauthTestState, oauthTestBrowserT} {
		if strings.Contains(haystack, secret) {
			t.Fatalf("the response echoed %q", secret)
		}
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer (the code would leak in the Referer)", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestCallbackClearsTheBindingCookie(t *testing.T) {
	svc := &fakeLinkService{}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet,
		oauthCallbackBase+"?code=c&state="+oauthTestState,
		bindingCookie(oauthTestState))

	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == handlers.MCPOAuthCookieName(false) && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the binding cookie was not cleared")
	}
}

func TestCallbackFailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		cookie *http.Cookie
		target string
		svcErr error
		calls  int
	}{
		"no cookie": {
			target: oauthCallbackBase + "?code=c&state=" + oauthTestState,
		},
		"cookie is for another state": {
			cookie: bindingCookie("a-different-state"),
			target: oauthCallbackBase + "?code=c&state=" + oauthTestState,
		},
		"no state in the query": {
			cookie: bindingCookie(oauthTestState),
			target: oauthCallbackBase + "?code=c",
		},
		"no code in the query": {
			cookie: bindingCookie(oauthTestState),
			target: oauthCallbackBase + "?state=" + oauthTestState,
		},
		"service refuses the transaction": {
			cookie: bindingCookie(oauthTestState),
			target: oauthCallbackBase + "?code=c&state=" + oauthTestState,
			svcErr: oauth.ErrBadTransaction,
			calls:  1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeLinkService{completeErr: tc.svcErr}
			rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet, tc.target, tc.cookie)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rec.Code)
			}
			if got := rec.Header().Get("Location"); !strings.Contains(got, "status=failed") {
				t.Fatalf("Location = %q, want status=failed", got)
			}
			if svc.completed != tc.calls {
				t.Fatalf("Complete called %d times, want %d", svc.completed, tc.calls)
			}
		})
	}
}

func TestUnlinkReturnsNoContent(t *testing.T) {
	svc := &fakeLinkService{}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodDelete, "/api/mcp/oauth/garmin", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if svc.unlinked != 1 {
		t.Fatalf("Unlink called %d times, want 1", svc.unlinked)
	}
}

func TestListReportsEachIntegration(t *testing.T) {
	expiry := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)
	svc := &fakeLinkService{states: []oauth.LinkState{
		{ServerID: oauthTestServerID, Linked: true, Status: store.LinkStatusLinked,
			Scope: testReadScope, AccessExpiresAt: expiry},
		{ServerID: "strava"},
	}}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet, "/api/mcp/integrations", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var envelope struct {
		Data []struct {
			Server          string `json:"server"`
			Linked          bool   `json:"linked"`
			Status          string `json:"status"`
			Scope           string `json:"scope"`
			AccessExpiresAt string `json:"access_expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if len(got) != 2 {
		t.Fatalf("got %d integrations, want 2", len(got))
	}
	if got[0].Server != oauthTestServerID || !got[0].Linked || got[0].Scope != testReadScope {
		t.Fatalf("linked entry = %+v", got[0])
	}
	if got[0].AccessExpiresAt != expiry.Format(time.RFC3339) {
		t.Fatalf("expiry = %q, want RFC 3339", got[0].AccessExpiresAt)
	}
	if got[1].Linked || got[1].Status != "" {
		t.Fatalf("unlinked entry = %+v", got[1])
	}
}

func TestCookieNameIsHostPrefixedOnlyWhenSecure(t *testing.T) {
	if got := handlers.MCPOAuthCookieName(true); !strings.HasPrefix(got, "__Host-") {
		t.Fatalf("production cookie = %q, want a __Host- prefix", got)
	}
	if got := handlers.MCPOAuthCookieName(false); strings.HasPrefix(got, "__Host-") {
		t.Fatalf("development cookie = %q; a __Host- cookie without Secure is rejected by browsers", got)
	}
}

func TestListReportsAScopeShortfall(t *testing.T) {
	svc := &fakeLinkService{states: []oauth.LinkState{
		{ServerID: oauthTestServerID, Linked: true, Status: store.LinkStatusLinked,
			Scope: testReadScope, ScopeShortfall: []string{"garmin:write"}},
	}}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet, "/api/mcp/integrations", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var envelope struct {
		Data []struct {
			ScopeShortfall []string `json:"scope_shortfall"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("got %d integrations, want 1", len(envelope.Data))
	}
	if got := envelope.Data[0].ScopeShortfall; len(got) != 1 || got[0] != "garmin:write" {
		t.Fatalf("scope_shortfall = %v, want [garmin:write]", got)
	}
}

func TestListOmitsAnEmptyScopeShortfall(t *testing.T) {
	svc := &fakeLinkService{states: []oauth.LinkState{
		{ServerID: oauthTestServerID, Linked: true, Status: store.LinkStatusLinked, Scope: testReadScope},
	}}
	rec := serveOAuth(t, newOAuthHandler(svc), http.MethodGet, "/api/mcp/integrations", nil)

	if strings.Contains(rec.Body.String(), "scope_shortfall") {
		t.Fatalf("a satisfied link still carried a shortfall: %s", rec.Body.String())
	}
}
