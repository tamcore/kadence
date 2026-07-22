package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	storepkg "github.com/tamcore/kadence/internal/store"
)

// testSessionCookieName mirrors the handlers package's unexported
// sessionCookie const ("session_id"); it cannot be imported directly since
// this file lives in package handlers_test.
const testSessionCookieName = "session_id"

// fakeSessionStore is a test double for the handlers.sessionStore seam.
type fakeSessionStore struct {
	list         []model.Session
	deleteErr    error
	othersExcept string
}

func (f *fakeSessionStore) ListByUser(_ context.Context, _ int64) ([]model.Session, error) {
	return f.list, nil
}

func (f *fakeSessionStore) DeleteByPublicIDForUser(_ context.Context, _ string, _ int64) error {
	return f.deleteErr
}

func (f *fakeSessionStore) DeleteOthersByUser(_ context.Context, _ int64, exceptID string) error {
	f.othersExcept = exceptID
	return nil
}

// doAuthedGetWithCookie builds an authed GET /api/sessions request with a
// session cookie attached, invokes fn, and returns the response body string.
func doAuthedGetWithCookie(t *testing.T, fn http.HandlerFunc, cookieValue string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u"}))
	req.AddCookie(&http.Cookie{Name: testSessionCookieName, Value: cookieValue})
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Body.String()
}

// doAuthedPostRevokeOthersWithCookie builds an authed POST
// /api/sessions/revoke-others request with a session cookie attached,
// invokes fn, and returns the status code.
func doAuthedPostRevokeOthersWithCookie(t *testing.T, fn http.HandlerFunc, cookieValue string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/revoke-others", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u"}))
	req.AddCookie(&http.Cookie{Name: testSessionCookieName, Value: cookieValue})
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Code
}

// doAuthedPostNoCookie builds an authed POST /api/sessions/revoke-others
// request without a session cookie, invokes fn, and returns the status code.
func doAuthedPostNoCookie(t *testing.T, fn http.HandlerFunc) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/revoke-others", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u"}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Code
}

func TestSessions_List_MarksCurrent_NoRawID(t *testing.T) {
	store := &fakeSessionStore{list: []model.Session{
		{ID: "SECRET-CURRENT", PublicID: "pub-1", UserAgent: "Mozilla/5.0 (Macintosh; Mac OS X) Chrome/120 Safari/537", IP: "1.1.1.1", CreatedAt: time.Now(), LastSeenAt: time.Now()},
		{ID: "SECRET-OTHER", PublicID: "pub-2", UserAgent: "", IP: "2.2.2.2", CreatedAt: time.Now(), LastSeenAt: time.Now()},
	}}
	h := handlers.NewSessions(store)
	body := doAuthedGetWithCookie(t, h.List, "SECRET-CURRENT")
	assertContains(t, body, `"publicId":"pub-1"`, `"current":true`, `"publicId":"pub-2"`)
	assertNotContains(t, body, "SECRET-CURRENT", "SECRET-OTHER")
}

func TestSessions_Revoke_OwnerScoped(t *testing.T) {
	store := &fakeSessionStore{deleteErr: storepkg.ErrNotFound}
	h := handlers.NewSessions(store)
	if code := doAuthedDeleteParam(t, h.Revoke, model.RoleUser, "publicId", "pub-x"); code != 404 {
		t.Fatalf("revoke non-owned code=%d want 404", code)
	}
}

func TestSessions_Revoke_Success(t *testing.T) {
	store := &fakeSessionStore{}
	h := handlers.NewSessions(store)
	if code := doAuthedDeleteParam(t, h.Revoke, model.RoleUser, "publicId", "pub-1"); code != http.StatusNoContent {
		t.Fatalf("revoke code=%d want %d", code, http.StatusNoContent)
	}
}

func TestSessions_RevokeOthers(t *testing.T) {
	if code := doAuthedPostNoCookie(t, handlers.NewSessions(&fakeSessionStore{}).RevokeOthers); code != 400 {
		t.Fatalf("no-cookie revoke-others code=%d want 400", code)
	}
	store := &fakeSessionStore{}
	_ = doAuthedPostRevokeOthersWithCookie(t, handlers.NewSessions(store).RevokeOthers, "SECRET-CURRENT")
	if store.othersExcept != "SECRET-CURRENT" {
		t.Fatalf("revoke-others exceptID=%q", store.othersExcept)
	}
}
