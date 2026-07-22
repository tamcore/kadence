package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitAllowsWithinLimitAndBlocksOverLimit(t *testing.T) {
	h := RateLimit(1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		return r
	}

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req())
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req())
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec2.Code)
	}
	if got := rec2.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header missing on 429 response")
	}
	if got := rec2.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var env struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&env); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if env.Error == "" {
		t.Fatal("429 response missing error envelope message")
	}
}

func TestRateLimitDifferentIPsGetSeparateBuckets(t *testing.T) {
	h := RateLimit(1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	reqFrom := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, reqFrom("10.0.0.1"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("ip1 request status = %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, reqFrom("10.0.0.2"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("ip2 request status = %d, want 200 (separate bucket from ip1)", rec2.Code)
	}
}

func TestRateLimitZeroOrNegativeDisables(t *testing.T) {
	for _, limit := range []int{0, -1} {
		h := RateLimit(limit)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "10.0.0.1:1234"
			return r
		}
		for i := range 5 {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req())
			if rec.Code != http.StatusOK {
				t.Fatalf("limit=%d request %d status = %d, want 200 (disabled)", limit, i, rec.Code)
			}
		}
	}
}
