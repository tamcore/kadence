package middleware_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/middleware"
)

func TestMaxBodyBytes_AllowsBodyAtOrUnderLimit(t *testing.T) {
	h := middleware.MaxBodyBytes(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("12345"))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "12345" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "12345")
	}
}

func TestMaxBodyBytes_OverLimitYieldsMaxBytesError(t *testing.T) {
	var readErr error
	h := middleware.MaxBodyBytes(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("123456"))
	h.ServeHTTP(rec, req)

	var maxBytesErr *http.MaxBytesError
	if !errors.As(readErr, &maxBytesErr) {
		t.Fatalf("read error = %v, want *http.MaxBytesError", readErr)
	}
}

func TestMaxBodyBytes_ZeroOrNegativeDisables(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		h := middleware.MaxBodyBytes(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
		}))

		rec := httptest.NewRecorder()
		payload := strings.Repeat("x", 4096)
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(payload))
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("limit=%d: status = %d, want 200 (disabled)", limit, rec.Code)
		}
		if rec.Body.String() != payload {
			t.Fatalf("limit=%d: body truncated, want full payload passed through", limit)
		}
	}
}
