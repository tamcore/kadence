package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSecurityHeadersSetsHeaders was superseded by securityheaders_test.go
// (SecurityHeaders is now a factory: SecurityHeaders(isProd, scriptHashes)),
// which covers the baseline headers plus the new CSP/HSTS behavior.

func TestAccessLogPassesThroughAndPreservesStatus(t *testing.T) {
	h := AccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}
