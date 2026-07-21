package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/api/middleware"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const (
	testSessionID     = "sid"
	testUsername      = "alice"
	testSessionCookie = "session_id"
)

type fakeSessions struct {
	s         model.Session
	touchedID *string
	touchedIP *string
	touchErr  error
}

func (f fakeSessions) GetByID(_ context.Context, id string) (model.Session, error) {
	if id == f.s.ID {
		return f.s, nil
	}
	return model.Session{}, store.ErrNotFound
}

func (f fakeSessions) Touch(_ context.Context, id string, ip string, _ time.Time) error {
	if f.touchedID != nil {
		*f.touchedID = id
	}
	if f.touchedIP != nil {
		*f.touchedIP = ip
	}
	return f.touchErr
}

type fakeUsers struct{ u model.User }

func (f fakeUsers) GetByID(_ context.Context, id int64) (model.User, error) {
	if id == f.u.ID {
		return f.u, nil
	}
	return model.User{}, store.ErrNotFound
}

func TestLoadUserPutsUserInContext(t *testing.T) {
	sess := model.Session{ID: testSessionID, UserID: 5, ExpiresAt: time.Now().Add(time.Hour)}
	usr := model.User{ID: 5, Username: testUsername, Role: model.RoleUser}
	mw := middleware.LoadUser(fakeSessions{s: sess}, fakeUsers{usr})

	var seen *model.User
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = auth.UserFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: testSessionCookie, Value: testSessionID})
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

func TestLoadUser_TouchesStaleSession(t *testing.T) {
	sess := model.Session{
		ID: testSessionID, UserID: 5, ExpiresAt: time.Now().Add(time.Hour),
		LastSeenAt: time.Now().Add(-10 * time.Minute),
	}
	usr := model.User{ID: 5, Username: testUsername, Role: model.RoleUser}
	var touchedID, touchedIP string
	fake := fakeSessions{s: sess, touchedID: &touchedID, touchedIP: &touchedIP}
	mw := middleware.LoadUser(fake, fakeUsers{usr})

	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: testSessionCookie, Value: testSessionID})
	req.RemoteAddr = "5.6.7.8:1111"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if touchedID != testSessionID {
		t.Errorf("touchedID = %q, want %q", touchedID, testSessionID)
	}
	if touchedIP != "5.6.7.8" {
		t.Errorf("touchedIP = %q, want %q", touchedIP, "5.6.7.8")
	}
}

func TestLoadUser_ProceedsWhenTouchFails(t *testing.T) {
	sess := model.Session{
		ID: testSessionID, UserID: 5, ExpiresAt: time.Now().Add(time.Hour),
		LastSeenAt: time.Now().Add(-10 * time.Minute),
	}
	usr := model.User{ID: 5, Username: testUsername, Role: model.RoleUser}
	fake := fakeSessions{s: sess, touchErr: errors.New("touch failed")}
	mw := middleware.LoadUser(fake, fakeUsers{usr})

	var seen *model.User
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = auth.UserFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: testSessionCookie, Value: testSessionID})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil || seen.ID != 5 {
		t.Fatalf("user not loaded despite touch error: %+v", seen)
	}
}

func TestLoadUser_SkipsFreshSession(t *testing.T) {
	sess := model.Session{
		ID: testSessionID, UserID: 5, ExpiresAt: time.Now().Add(time.Hour),
		LastSeenAt: time.Now(),
	}
	usr := model.User{ID: 5, Username: testUsername, Role: model.RoleUser}
	var touchedID string
	fake := fakeSessions{s: sess, touchedID: &touchedID}
	mw := middleware.LoadUser(fake, fakeUsers{usr})

	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: testSessionCookie, Value: testSessionID})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if touchedID != "" {
		t.Errorf("touchedID = %q, want empty (no touch)", touchedID)
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
