package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
)

// mustNewRouter builds a router and fails the test if construction errors.
func mustNewRouter(t *testing.T, deps Deps) http.Handler {
	t.Helper()
	r, err := NewRouter(deps)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func TestHealthzReturnsOK(t *testing.T) {
	srv := httptest.NewServer(mustNewRouter(t, Deps{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/healthz")
	if err != nil {
		t.Fatalf("GET /api/healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != contentTypeJSON {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("security headers not applied: X-Content-Type-Options = %q", got)
	}
}

func TestRootServesPlaceholderWhenNoFrontend(t *testing.T) {
	// In `go test` builds (no prodfrontend tag) web.Available() is false,
	// so the root serves the placeholder page.
	srv := httptest.NewServer(mustNewRouter(t, Deps{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestNewRouterFailsOnProdWithoutCSRFSecret ensures router construction
// fails loudly instead of falling back to a per-restart random CSRF secret
// in production. Config.Validate() already rejects this configuration
// before startup, but NewRouter does not rely on an upstream caller having
// run it.
func TestNewRouterFailsOnProdWithoutCSRFSecret(t *testing.T) {
	deps := Deps{
		Users:    store.NewUserRepository(nil),
		Sessions: store.NewSessionRepository(nil),
		Config:   config.Config{Env: "prod", CSRFSecret: ""},
	}
	if _, err := NewRouter(deps); err == nil {
		t.Fatal("NewRouter should fail for a prod config with an empty CSRF secret")
	}
}

// TestNewRouterSucceedsOnProdWithCSRFSecret is the control case: a prod
// config with a secret set must still construct successfully.
func TestNewRouterSucceedsOnProdWithCSRFSecret(t *testing.T) {
	deps := Deps{
		Users:    store.NewUserRepository(nil),
		Sessions: store.NewSessionRepository(nil),
		Config:   config.Config{Env: "prod", CSRFSecret: "0123456789abcdef0123456789abcdef"},
	}
	if _, err := NewRouter(deps); err != nil {
		t.Fatalf("NewRouter should succeed with a CSRF secret set: %v", err)
	}
}
