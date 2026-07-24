package api_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
)

// publicAllowlist is the exact set of routes reachable without an
// authenticated session, per WAVE2-hardening.md §10. Every other route
// registered on the router must reject an anonymous request with 401.
var publicAllowlist = map[string]bool{
	"GET /api/healthz":                true,
	"POST /api/session":               true,
	"POST /api/webauthn/login/begin":  true,
	"POST /api/webauthn/login/finish": true,
	"GET /api/webauthn/enabled":       true,
}

// routeParam matches a chi path parameter placeholder, e.g. "{id}".
var routeParam = regexp.MustCompile(`\{[^}]+\}`)

// fullDeps builds Deps with every handler populated (backed by nil-safe,
// never-invoked-for-anonymous-requests fakes/repos) so chi.Walk discovers the
// full route table, including handlers gated behind optional Deps fields.
func fullDeps() api.Deps {
	users := store.NewUserRepository(nil)
	sessions := store.NewSessionRepository(nil)
	cfg := config.Config{}

	return api.Deps{
		Users:       users,
		Sessions:    sessions,
		Config:      cfg,
		Chat:        handlers.NewChat(nil, nil, nil),
		Documents:   handlers.NewDocuments(nil, nil, 0),
		Context:     handlers.NewContext(nil, nil),
		Credentials: handlers.NewCredentials(nil),
		MCP:         handlers.NewMCP(nil, nil, nil, false, 10),
		Profile:     handlers.NewProfile(nil, nil, cfg),
		SessionsAPI: handlers.NewSessions(nil),
		WebAuthn:    handlers.NewWebAuthn(nil, nil, nil, nil, nil, cfg),
		Scheduled:   handlers.NewScheduled(nil),
	}
}

// TestRouterWalk_AnonymousRequestsRejectedExceptAllowlist is the router-walk
// invariant test: every route registered on the router must 401 an anonymous
// (no session cookie) request, except the small public allowlist (healthz,
// login, passkey login, webauthn-enabled probe).
func TestRouterWalk_AnonymousRequestsRejectedExceptAllowlist(t *testing.T) {
	router := api.NewRouter(fullDeps())
	chiRouter, ok := router.(chi.Router)
	if !ok {
		t.Fatalf("NewRouter() = %T, want chi.Router", router)
	}

	srv := httptest.NewServer(router)
	defer srv.Close()

	seen := map[string]bool{}
	err := chi.Walk(chiRouter, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") {
			return nil
		}
		key := method + " " + route
		seen[key] = true

		urlPath := routeParam.ReplaceAllString(route, "x")
		req, reqErr := http.NewRequest(method, srv.URL+urlPath, nil)
		if reqErr != nil {
			t.Fatalf("build request %s %s: %v", method, urlPath, reqErr)
		}
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("%s %s: %v", method, urlPath, doErr)
		}
		defer func() { _ = resp.Body.Close() }()

		if publicAllowlist[key] {
			if resp.StatusCode == http.StatusUnauthorized {
				t.Errorf("%s: got 401, want a public route to NOT require auth", key)
			}
			return nil
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (anonymous request to a non-allowlisted route)", key, resp.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	for key := range publicAllowlist {
		if !seen[key] {
			t.Errorf("allowlisted route %q was never registered on the router", key)
		}
	}
	for _, key := range []string{
		"GET /api/scheduled/tasks", "POST /api/scheduled/tasks", "GET /api/scheduled/tasks/{id}",
		"PATCH /api/scheduled/tasks/{id}", "DELETE /api/scheduled/tasks/{id}",
		"POST /api/scheduled/tasks/{id}/messages", "POST /api/scheduled/tasks/{id}/confirm",
		"POST /api/scheduled/tasks/{id}/run", "POST /api/scheduled/tasks/{id}/read",
	} {
		if !seen[key] {
			t.Errorf("scheduled route %q was never registered", key)
		}
	}
}
