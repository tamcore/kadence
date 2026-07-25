package api

// This file lives in package api (not api_test) specifically to reach the
// unexported isUploadRoute predicate and prove, without a database, that the
// document-upload routes are wired outside the global MaxBodyBytes cap.
//
// A full end-to-end proof (authenticated upload over the global cap but
// under UploadMaxBytes) needs a real session, which needs a DB — that's
// TestBodyLimit_GlobalCapAppliesButUploadOverrides in
// router_integration_test.go (Docker-gated via testutil.SetupTestDB). The
// tests below instead exercise exactly the two pieces NewRouter wires
// together for this exemption — the isUploadRoute predicate and
// middleware.MaxBodyBytesExempt — directly, with no auth/DB involved, and
// show:
//  1. isUploadRoute matches only POST to the two upload paths.
//  2. Wrapping a handler with MaxBodyBytesExempt(tinyLimit, isUploadRoute) (the
//     exact construction NewRouter uses) lets an oversized body reach the
//     handler unmodified for upload routes, while any other route still gets
//     capped. This is the structural guarantee that the outer group-level cap
//     can never nest a smaller http.MaxBytesReader in front of the handler's
//     own, larger UploadMaxBytes check for upload routes.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/middleware"
)

func TestIsUploadRoute(t *testing.T) {
	cases := []struct {
		method      string
		path        string
		contentType string
		want        bool
	}{
		{http.MethodPost, documentsPath, "multipart/form-data; boundary=x", true},
		{http.MethodPost, adminDocumentsPath, "multipart/form-data; boundary=x", true},
		{http.MethodGet, documentsPath, "", false},
		{http.MethodDelete, documentsPath + "/1", "", false},
		{http.MethodGet, adminDocumentsPath, "", false},
		{http.MethodPost, "/api/profile", "application/json", false},
		{http.MethodPost, "/api/chat", "multipart/form-data; boundary=x", true},
		{http.MethodPost, "/api/chat", "application/json", false},
		{http.MethodPost, "/api/chat", "", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		if got := isUploadRoute(req); got != tc.want {
			t.Errorf(
				"isUploadRoute(%s %s, %q) = %v, want %v",
				tc.method, tc.path, tc.contentType, got, tc.want,
			)
		}
	}
}

// TestMaxBodyBytesExempt_UploadRoutesBypassGlobalCap proves the router-level
// wiring: the exact middleware construction NewRouter installs
// (middleware.MaxBodyBytesExempt(cap, isUploadRoute)) does not truncate/limit
// bodies on the upload routes, even when the global cap is far smaller than
// the body sent — while a non-upload route with the same construction is
// still capped. This is the "outside the MaxBodyBytes group" guarantee,
// verified at the middleware level rather than by standing up the full
// authenticated router (which needs a DB).
func TestMaxBodyBytesExempt_UploadRoutesBypassGlobalCap(t *testing.T) {
	const globalCap = 16 // bytes
	const bodySize = 2 * 1024 * 1024

	handler := middleware.MaxBodyBytesExempt(globalCap, isUploadRoute)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{byte(len(b))}) // presence check only
		}),
	)

	large := strings.Repeat("a", bodySize)

	for _, path := range []string{documentsPath, adminDocumentsPath} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(large))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (upload route must bypass the %d-byte global cap)", path, rec.Code, globalCap)
		}
	}

	// Multipart chat bypasses the global JSON cap; the chat handler enforces
	// its own aggregate file cap.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(large))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/api/chat multipart: status = %d, want 200", rec.Code)
	}

	// Control: the same construction still enforces the cap on a non-upload route.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/profile", strings.NewReader(large))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("/api/profile: status = %d, want 413 (non-upload route must stay capped at %d bytes)", rec.Code, globalCap)
	}

	// JSON chat stays behind the global cap.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(large))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("/api/chat JSON: status = %d, want 413", rec.Code)
	}
}
