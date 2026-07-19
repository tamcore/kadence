package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// statusRecorder must expose the wrapped ResponseWriter via Unwrap so that
// http.ResponseController can reach the underlying Flusher — otherwise SSE
// streaming (chat) fails with http.ErrNotSupported ("feature not supported").
func TestStatusRecorderFlushable(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	if err := http.NewResponseController(rec).Flush(); err != nil {
		t.Fatalf("Flush through statusRecorder failed: %v", err)
	}
}
