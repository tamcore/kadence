package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamcore/kadence/internal/api/middleware"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
)

func TestRequireAdmin(t *testing.T) {
	h := middleware.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// non-admin → 403
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Role: model.RoleUser}))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user status = %d, want 403", rec.Code)
	}

	// admin → 200
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 2, Role: model.RoleAdmin}))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200", rec.Code)
	}
}
