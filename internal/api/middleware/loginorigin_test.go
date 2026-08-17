package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/middleware"
)

const trustedOrigin = "https://kadence.example.invalid"

func loginGuard(t *testing.T, isProd bool) http.Handler {
	t.Helper()
	reached := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return middleware.RequireLoginOrigin([]string{trustedOrigin}, isProd)(reached)
}

func loginRequest(contentType, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://kadence.example.invalid/api/session",
		strings.NewReader(`{"username":"alice","password":"x"}`))
	req.Host = "kadence.example.invalid"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func TestLoginGuardRefusesACrossSiteForm(t *testing.T) {
	// These are the shapes a cross-origin HTML form can actually send: simple
	// content types the browser allows without a preflight.
	for _, contentType := range []string{
		"text/plain;charset=UTF-8",
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x",
		"",
	} {
		rec := httptest.NewRecorder()
		loginGuard(t, true).ServeHTTP(rec, loginRequest(contentType, "https://evil.example.invalid"))
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("content type %q: status = %d, want 415", contentType, rec.Code)
		}
	}
}

func TestLoginGuardRefusesAnUntrustedOrigin(t *testing.T) {
	rec := httptest.NewRecorder()
	loginGuard(t, true).ServeHTTP(rec, loginRequest(jsonContentTypeForTest, "https://evil.example.invalid"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

const jsonContentTypeForTest = "application/json"

func TestLoginGuardAllowsTheApplicationItself(t *testing.T) {
	for name, origin := range map[string]string{
		"configured trusted origin": trustedOrigin,
		"trailing slash":            trustedOrigin + "/",
	} {
		rec := httptest.NewRecorder()
		loginGuard(t, true).ServeHTTP(rec, loginRequest(jsonContentTypeForTest, origin))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, rec.Code)
		}
	}
}

func TestLoginGuardAllowsAClientThatSendsNoOrigin(t *testing.T) {
	rec := httptest.NewRecorder()
	loginGuard(t, true).ServeHTTP(rec, loginRequest("application/json; charset=utf-8", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a non-browser client sends no Origin)", rec.Code)
	}
}

func TestLoginGuardAllowsThePlainHTTPDevOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:5173/api/session", strings.NewReader("{}"))
	req.Host = "localhost:5173"
	req.Header.Set("Content-Type", jsonContentTypeForTest)
	req.Header.Set("Origin", "http://localhost:5173")

	rec := httptest.NewRecorder()
	loginGuard(t, false).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; the dev server is served over http", rec.Code)
	}
}
