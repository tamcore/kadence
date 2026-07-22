package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
)

// rateLimitDeps builds router deps with non-nil (unconnected) repositories so
// mountAuth registers the auth routes, without requiring a database. Routes
// that would touch the repositories are never reached in these tests because
// the rate limiter (or the absence of a session cookie) short-circuits first.
func rateLimitDeps(cfg config.Config) api.Deps {
	return api.Deps{
		Users:    store.NewUserRepository(nil),
		Sessions: store.NewSessionRepository(nil),
		Config:   cfg,
	}
}

func TestGlobalRateLimitReturns429AfterCap(t *testing.T) {
	srv := httptest.NewServer(api.NewRouter(rateLimitDeps(config.Config{RateLimitGlobal: 1})))
	defer srv.Close()

	resp1, err := http.Get(srv.URL + "/api/session")
	if err != nil {
		t.Fatalf("request 1: %v", err)
	}
	_ = resp1.Body.Close()
	if resp1.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("first request already rate limited")
	}

	resp2, err := http.Get(srv.URL + "/api/session")
	if err != nil {
		t.Fatalf("request 2: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once global cap of 1 req/min is exceeded", resp2.StatusCode)
	}
	assertRateLimitResponse(t, resp2)
}

func TestGlobalRateLimitZeroDisables(t *testing.T) {
	srv := httptest.NewServer(api.NewRouter(rateLimitDeps(config.Config{RateLimitGlobal: 0})))
	defer srv.Close()

	for i := range 5 {
		resp, err := http.Get(srv.URL + "/api/session")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d got 429, want global rate limiting disabled when RateLimitGlobal=0", i)
		}
	}
}

func TestHealthzNeverRateLimited(t *testing.T) {
	srv := httptest.NewServer(api.NewRouter(rateLimitDeps(config.Config{RateLimitGlobal: 1})))
	defer srv.Close()

	// Exhaust the global cap of 1 req/min via a limited route first.
	resp, _ := http.Get(srv.URL + "/api/session")
	_ = resp.Body.Close()

	for i := range 5 {
		hz, err := http.Get(srv.URL + "/api/healthz")
		if err != nil {
			t.Fatalf("healthz request %d: %v", i, err)
		}
		_ = hz.Body.Close()
		if hz.StatusCode != http.StatusOK {
			t.Fatalf("healthz request %d = %d, want 200 (healthz must stay outside the global limiter)", i, hz.StatusCode)
		}
	}
}

func TestAuthStrictRateLimitReturns429OnLoginEndpoint(t *testing.T) {
	srv := httptest.NewServer(api.NewRouter(rateLimitDeps(config.Config{RateLimitGlobal: 1000, RateLimitAuth: 2})))
	defer srv.Close()

	var last *http.Response
	for i := range 3 {
		resp, err := http.Post(srv.URL+"/api/session", "application/json",
			strings.NewReader(`{"username":"nobody","password":"whatever","remember":false}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if i < 2 {
			_ = resp.Body.Close()
			continue
		}
		last = resp
	}
	defer func() { _ = last.Body.Close() }()

	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("3rd login attempt/min status = %d, want 429 (auth limit is 2/min)", last.StatusCode)
	}
	assertRateLimitResponse(t, last)
}

func TestAuthStrictRateLimitZeroDisables(t *testing.T) {
	srv := httptest.NewServer(api.NewRouter(rateLimitDeps(config.Config{RateLimitGlobal: 1000, RateLimitAuth: 0})))
	defer srv.Close()

	for i := range 5 {
		resp, err := http.Post(srv.URL+"/api/session", "application/json",
			strings.NewReader(`{"username":"nobody","password":"whatever","remember":false}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d got 429, want auth rate limiting disabled when RateLimitAuth=0", i)
		}
	}
}

func assertRateLimitResponse(t *testing.T, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header missing on 429 response")
	}
	var env struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if env.Error == "" {
		t.Fatal("429 response missing error envelope message")
	}
}
