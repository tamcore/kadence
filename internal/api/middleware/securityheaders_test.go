package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersBaseline(t *testing.T) {
	h := SecurityHeaders(false, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}
}

func TestSecurityHeadersDevPolicyWithoutHashes(t *testing.T) {
	h := SecurityHeaders(false, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp != devCSP {
		t.Fatalf("Content-Security-Policy = %q, want dev fallback %q", csp, devCSP)
	}
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Fatalf("Strict-Transport-Security = %q, want empty outside prod", hsts)
	}
}

func TestSecurityHeadersStrictPolicyWithHashes(t *testing.T) {
	hashes := []string{"sha256-AAAA", "sha256-BBBB"}
	h := SecurityHeaders(true, hashes)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	wantParts := []string{
		"default-src 'self'",
		"script-src 'self' 'sha256-AAAA' 'sha256-BBBB'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	for _, part := range wantParts {
		if !strings.Contains(csp, part) {
			t.Fatalf("Content-Security-Policy = %q, missing part %q", csp, part)
		}
	}

	hsts := rec.Header().Get("Strict-Transport-Security")
	if hsts != "max-age=31536000; includeSubDomains" {
		t.Fatalf("Strict-Transport-Security = %q, want max-age=31536000; includeSubDomains", hsts)
	}
}

func TestSecurityHeadersProdWithoutHashesDegradesToDevPolicy(t *testing.T) {
	// A prod build somehow missing its hashes file must never ship a broken
	// strict CSP (script-src 'self' with no hash would block the SPA's own
	// inline bootstrap script) — it falls back to the permissive dev policy.
	h := SecurityHeaders(true, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if csp := rec.Header().Get("Content-Security-Policy"); csp != devCSP {
		t.Fatalf("Content-Security-Policy = %q, want dev fallback %q", csp, devCSP)
	}
	// HSTS is a transport-layer guarantee independent of the CSP degrade; it
	// still reflects cfg.IsProd().
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts != "max-age=31536000; includeSubDomains" {
		t.Fatalf("Strict-Transport-Security = %q, want max-age=31536000; includeSubDomains", hsts)
	}
}
