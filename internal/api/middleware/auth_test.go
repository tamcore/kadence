package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/api/middleware"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

type fakeSessions struct{ s model.Session }

func (f fakeSessions) GetByID(_ context.Context, id string) (model.Session, error) {
	if id == f.s.ID {
		return f.s, nil
	}
	return model.Session{}, store.ErrNotFound
}

type fakeUsers struct{ u model.User }

func (f fakeUsers) GetByID(_ context.Context, id int64) (model.User, error) {
	if id == f.u.ID {
		return f.u, nil
	}
	return model.User{}, store.ErrNotFound
}

func TestLoadUserPutsUserInContext(t *testing.T) {
	sess := model.Session{ID: "sid", UserID: 5, ExpiresAt: time.Now().Add(time.Hour)}
	usr := model.User{ID: 5, Username: "alice", Role: model.RoleUser}
	mw := middleware.LoadUser(fakeSessions{sess}, fakeUsers{usr})

	var seen *model.User
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = auth.UserFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "sid"})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil || seen.ID != 5 {
		t.Fatalf("user not loaded: %+v", seen)
	}
}

func TestLoadUserNoCookieProceedsAnonymous(t *testing.T) {
	mw := middleware.LoadUser(fakeSessions{}, fakeUsers{})
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if auth.UserFromContext(r.Context()) != nil {
			t.Fatal("expected no user")
		}
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("next handler not called")
	}
}

func TestRequireAuthBlocksAnonymous(t *testing.T) {
	h := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
