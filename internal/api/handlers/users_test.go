package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const (
	testEditUser             = "bob"
	testEditEmail            = "b@x.io"
	testActingAdminUsername  = "admin"
	testCurrentSessionCookie = "current-session-id"
)

// sessionsFake is a fake handlers.UsersSessions used to observe revocation
// calls made by Users.Update without a real database.
type sessionsFake struct {
	allByUserCalls    []int64
	othersUserCalls   []int64
	othersExceptCalls []string
}

func (s *sessionsFake) DeleteAllByUser(_ context.Context, userID int64) error {
	s.allByUserCalls = append(s.allByUserCalls, userID)
	return nil
}

func (s *sessionsFake) DeleteOthersByUser(_ context.Context, userID int64, exceptID string) error {
	s.othersUserCalls = append(s.othersUserCalls, userID)
	s.othersExceptCalls = append(s.othersExceptCalls, exceptID)
	return nil
}

type usersRepo struct {
	list       []model.User
	created    *model.User
	deleted    int64
	byID       map[int64]model.User
	updated    *model.User
	pwUpdated  string
	adminCount int
}

func (r *usersRepo) Create(_ context.Context, u model.User) (model.User, error) {
	u.ID = 10
	r.created = &u
	return u, nil
}
func (r *usersRepo) ListAll(context.Context) ([]model.User, error) { return r.list, nil }
func (r *usersRepo) Delete(_ context.Context, id int64) error      { r.deleted = id; return nil }
func (r *usersRepo) Count(context.Context) (int, error)            { return len(r.list), nil }

func (r *usersRepo) GetByID(_ context.Context, id int64) (model.User, error) {
	if u, ok := r.byID[id]; ok {
		return u, nil
	}
	return model.User{}, store.ErrNotFound
}
func (r *usersRepo) UpdateUser(_ context.Context, id int64, username, email, role string) (model.User, error) {
	u := model.User{ID: id, Username: username, Email: email, Role: role}
	r.updated = &u
	return u, nil
}
func (r *usersRepo) UpdatePassword(_ context.Context, _ int64, hash string) error {
	r.pwUpdated = hash
	return nil
}
func (r *usersRepo) CountAdmins(context.Context) (int, error) { return r.adminCount, nil }

// patchUser issues a PATCH /api/users/{id} against a router wired to h.Update.
func patchUser(t *testing.T, h *handlers.Users, id, body string) *http.Response {
	t.Helper()
	r := chi.NewRouter()
	r.Patch("/api/users/{id}", h.Update)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/users/"+id, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// patchUserAs issues an in-process PATCH /api/users/{id} carrying an
// authenticated acting-admin context (and, when non-empty, a session
// cookie), so Users.Update can tell "admin resets their own password" apart
// from "admin resets someone else's". Unlike patchUser it drives the chi
// router directly (no real HTTP round trip), so req.WithContext survives
// into the handler while chi still resolves the {id} URL param.
func patchUserAs(t *testing.T, h *handlers.Users, id, body string, actingID int64, cookieValue string) *http.Response {
	t.Helper()
	r := chi.NewRouter()
	r.Patch("/api/users/{id}", h.Update)
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+id, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: actingID, Username: testActingAdminUsername}))
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: testSessionCookieName, Value: cookieValue})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Result()
}

func TestCreateUserHashesAndInserts(t *testing.T) {
	repo := &usersRepo{}
	h := handlers.NewUsers(repo, &sessionsFake{})
	body := `{"username":"bob","email":"b@x.io","password":"password123","role":"user"}`
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.PasswordHash == "password123" || repo.created.Role != "user" {
		t.Fatalf("bad create: %+v", repo.created)
	}
}

func TestCreateUserRejectsShortPassword(t *testing.T) {
	repo := &usersRepo{}
	h := handlers.NewUsers(repo, &sessionsFake{})
	body := `{"username":"bob","email":"b@x.io","password":"short7c","role":"user"}`
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if repo.created != nil {
		t.Fatalf("user should not have been created on short password, got %+v", repo.created)
	}
}

func TestCreateUserRejectsBadRole(t *testing.T) {
	h := handlers.NewUsers(&usersRepo{}, &sessionsFake{})
	body := `{"username":"bob","email":"b@x.io","password":"pw","role":"superadmin"}`
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func deleteUser(t *testing.T, h *handlers.Users, id string) (*http.Response, func()) {
	t.Helper()
	r := chi.NewRouter()
	r.Delete("/api/users/{id}", h.Delete)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/users/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp, func() { _ = resp.Body.Close() }
}

func TestDeleteUserParsesID(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{42: {ID: 42, Role: model.RoleUser}}}
	resp, done := deleteUser(t, handlers.NewUsers(repo, &sessionsFake{}), "42")
	defer done()
	if resp.StatusCode != http.StatusOK || repo.deleted != 42 {
		t.Fatalf("status=%d deleted=%d", resp.StatusCode, repo.deleted)
	}
}

func TestDeleteBlocksLastAdmin(t *testing.T) {
	repo := &usersRepo{
		byID:       map[int64]model.User{1: {ID: 1, Role: model.RoleAdmin}},
		adminCount: 1,
	}
	resp, done := deleteUser(t, handlers.NewUsers(repo, &sessionsFake{}), "1")
	defer done()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if repo.deleted != 0 {
		t.Fatalf("delete should not have proceeded, deleted=%d", repo.deleted)
	}
}

func TestUpdateUserEditsFieldsAndResetsPassword(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{5: {ID: 5, Username: testEditUser, Email: testEditEmail, Role: model.RoleUser}}}
	h := handlers.NewUsers(repo, &sessionsFake{})
	resp := patchUser(t, h, "5", `{"username":"bobby","email":"new@x.io","role":"admin","password":"longenough"}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if repo.updated == nil || repo.updated.Username != "bobby" || repo.updated.Email != "new@x.io" || repo.updated.Role != model.RoleAdmin {
		t.Fatalf("bad update: %+v", repo.updated)
	}
	if repo.pwUpdated == "" || repo.pwUpdated == "longenough" {
		t.Fatalf("password not hashed/updated: %q", repo.pwUpdated)
	}
}

func TestUpdateUserBlankPasswordLeavesItUnchanged(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{5: {ID: 5, Username: testEditUser, Email: testEditEmail, Role: model.RoleUser}}}
	h := handlers.NewUsers(repo, &sessionsFake{})
	resp := patchUser(t, h, "5", `{"username":"bob","email":"b@x.io","role":"user"}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if repo.pwUpdated != "" {
		t.Fatalf("password should be untouched, got %q", repo.pwUpdated)
	}
}

func TestUpdateUserRejectsBadRole(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{5: {ID: 5, Role: model.RoleUser}}}
	resp := patchUser(t, handlers.NewUsers(repo, &sessionsFake{}), "5", `{"username":"b","email":"b@x.io","role":"root"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateUserRejectsShortPassword(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{5: {ID: 5, Role: model.RoleUser}}}
	resp := patchUser(t, handlers.NewUsers(repo, &sessionsFake{}), "5", `{"username":"b","email":"b@x.io","role":"user","password":"short"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if repo.pwUpdated != "" {
		t.Fatalf("password should not be updated on validation failure")
	}
}

func TestUpdateUserNotFound(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{}}
	resp := patchUser(t, handlers.NewUsers(repo, &sessionsFake{}), "99", `{"username":"b","email":"b@x.io","role":"user"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateUserBlocksDemotingLastAdmin(t *testing.T) {
	repo := &usersRepo{
		byID:       map[int64]model.User{1: {ID: 1, Username: testActingAdminUsername, Email: "admin@x.io", Role: model.RoleAdmin}},
		adminCount: 1,
	}
	resp := patchUser(t, handlers.NewUsers(repo, &sessionsFake{}), "1", `{"username":"admin","email":"admin@x.io","role":"user"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if repo.updated != nil {
		t.Fatalf("update should not have proceeded")
	}
}

func TestUpdatePasswordRevokesTargetSessions(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{5: {ID: 5, Username: testEditUser, Email: testEditEmail, Role: model.RoleUser}}}
	sessions := &sessionsFake{}
	h := handlers.NewUsers(repo, sessions)
	resp := patchUserAs(t, h, "5", `{"username":"bobby","email":"new@x.io","role":"user","password":"longenough"}`, 1, "admin-current-session")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(sessions.allByUserCalls) != 1 || sessions.allByUserCalls[0] != 5 {
		t.Fatalf("expected DeleteAllByUser(5), got all=%v others=%v", sessions.allByUserCalls, sessions.othersUserCalls)
	}
	if len(sessions.othersUserCalls) != 0 {
		t.Fatalf("did not expect DeleteOthersByUser, got %v", sessions.othersUserCalls)
	}
}

func TestUpdatePasswordSelfResetKeepsCurrentSession(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{
		1: {ID: 1, Username: testActingAdminUsername, Email: "admin@x.io", Role: model.RoleAdmin},
	}, adminCount: 2}
	sessions := &sessionsFake{}
	h := handlers.NewUsers(repo, sessions)
	resp := patchUserAs(t, h, "1", `{"username":"admin","email":"admin@x.io","role":"admin","password":"longenough"}`, 1, testCurrentSessionCookie)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(sessions.allByUserCalls) != 0 {
		t.Fatalf("did not expect DeleteAllByUser, got %v", sessions.allByUserCalls)
	}
	if len(sessions.othersUserCalls) != 1 || sessions.othersUserCalls[0] != 1 || sessions.othersExceptCalls[0] != testCurrentSessionCookie {
		t.Fatalf("expected DeleteOthersByUser(1, %s), got users=%v except=%v",
			testCurrentSessionCookie, sessions.othersUserCalls, sessions.othersExceptCalls)
	}
}

func TestUpdateNoPasswordChangeDoesNotRevoke(t *testing.T) {
	repo := &usersRepo{byID: map[int64]model.User{5: {ID: 5, Username: testEditUser, Email: testEditEmail, Role: model.RoleUser}}}
	sessions := &sessionsFake{}
	h := handlers.NewUsers(repo, sessions)
	resp := patchUserAs(t, h, "5", `{"username":"bob","email":"b@x.io","role":"user"}`, 1, "")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(sessions.allByUserCalls)+len(sessions.othersUserCalls) != 0 {
		t.Fatalf("no revocation expected when password is unchanged: all=%v others=%v",
			sessions.allByUserCalls, sessions.othersUserCalls)
	}
}
