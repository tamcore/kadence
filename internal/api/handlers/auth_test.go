package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

type userGetter struct{ u model.User }

func (g userGetter) GetByUsername(_ context.Context, name string) (model.User, error) {
	if name == g.u.Username {
		return g.u, nil
	}
	return model.User{}, store.ErrNotFound
}

type sessionStore struct{ created, deleted string }

func (s *sessionStore) Create(_ context.Context, sess model.Session) error {
	s.created = sess.ID
	return nil
}
func (s *sessionStore) Delete(_ context.Context, id string) error { s.deleted = id; return nil }

func newAuth(t *testing.T, pw string) (*handlers.Auth, *sessionStore) {
	t.Helper()
	hash, _ := auth.HashPassword(pw)
	u := model.User{ID: 1, Username: "alice", Email: testEmail, PasswordHash: hash, Role: model.RoleUser}
	ss := &sessionStore{}
	return handlers.NewAuth(config.Config{}, userGetter{u}, ss), ss
}

func TestLoginSuccessSetsCookie(t *testing.T) {
	h, ss := newAuth(t, "hunter2")
	body := `{"username":"alice","password":"hunter2","remember":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if ss.created == "" {
		t.Fatal("session not created")
	}
	var setCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == testSessionCookieName {
			setCookie = c.Value
		}
	}
	if setCookie == "" {
		t.Fatal("session_id cookie not set")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	h, _ := newAuth(t, "hunter2")
	body := `{"username":"alice","password":"WRONG","remember":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLoginUnknownUserRunsDummyCompareAndMatchesWrongPasswordResponse(t *testing.T) {
	h, _ := newAuth(t, "hunter2")

	unknownBody := `{"username":"nope","password":"whatever","remember":false}`
	start := time.Now()
	rec := httptest.NewRecorder()
	h.Login(rec, httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(unknownBody)))
	elapsed := time.Since(start)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// bcrypt.DefaultCost takes tens of milliseconds; a near-zero elapsed time
	// means the unknown-user path short-circuited without running the dummy
	// compare, which is exactly the timing oracle this test guards against.
	if elapsed < 5*time.Millisecond {
		t.Fatalf("unknown-user login returned in %s; want a bcrypt-comparable delay (dummy compare not executed?)", elapsed)
	}

	wrongBody := `{"username":"alice","password":"WRONG","remember":false}`
	rec2 := httptest.NewRecorder()
	h.Login(rec2, httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(wrongBody)))

	if rec.Body.String() != rec2.Body.String() {
		t.Fatalf("unknown-user body %q != wrong-password body %q; response must not leak username validity",
			rec.Body.String(), rec2.Body.String())
	}
}

// TestCurrentUserReturnsContextUser covers CurrentUser's happy path. The
// anonymous (no user in context) case is now a router-level invariant
// enforced by middleware.RequireAuth ahead of this handler — see
// TestRequireAuthBlocksAnonymous and TestRouterWalk_AnonymousRequestsRejectedExceptAllowlist.
func TestCurrentUserReturnsContextUser(t *testing.T) {
	h, _ := newAuth(t, "x")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 9, Username: "bob"}))
	h.CurrentUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var env struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Username != "bob" {
		t.Fatalf("bad current user: %s", rec.Body.String())
	}
}
