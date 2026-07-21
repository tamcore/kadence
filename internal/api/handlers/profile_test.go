package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const testUsername = "alice"

type fakeProfileUsers struct {
	user            model.User
	updateErr       error
	passwordUpdated bool
}

func (f *fakeProfileUsers) GetByID(_ context.Context, _ int64) (model.User, error) {
	return f.user, nil
}

func (f *fakeProfileUsers) UpdateProfile(_ context.Context, _ int64, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeProfileUsers) UpdatePassword(_ context.Context, _ int64, _ string) error {
	f.passwordUpdated = true
	return nil
}

type fakeProfileSessions struct {
	deleteOthersCalled bool
	exceptID           string
	deleteErr          error
}

func (f *fakeProfileSessions) DeleteOthersByUser(_ context.Context, _ int64, exceptID string) error {
	f.deleteOthersCalled = true
	f.exceptID = exceptID
	return f.deleteErr
}

// doAuthedPatch issues an authenticated PATCH request against fn and returns the status code.
func doAuthedPatch(t *testing.T, fn http.HandlerFunc, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/profile", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: testUsername}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec.Code
}

// doAuthedPostWithCookie issues an authenticated POST request (with a session
// cookie attached) against fn and returns the status code.
func doAuthedPostWithCookie(t *testing.T, fn http.HandlerFunc, body string) int {
	t.Helper()
	return doAuthedPostRaw(t, fn, body).Code
}

// doAuthedPostBodyWithCookie is like doAuthedPostWithCookie but returns the response body.
func doAuthedPostBodyWithCookie(t *testing.T, fn http.HandlerFunc, body string) string {
	t.Helper()
	return doAuthedPostRaw(t, fn, body).Body.String()
}

func doAuthedPostRaw(t *testing.T, fn http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/profile/password", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: testUsername}))
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "current-session-id"})
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func TestProfile_Update_EmailConflictAndUnitValidation(t *testing.T) {
	users := &fakeProfileUsers{updateErr: store.ErrEmailTaken}
	h := handlers.NewProfile(users, &fakeProfileSessions{}, config.Config{})
	if code := doAuthedPatch(t, h.Update, `{"displayName":"A","email":"taken@x.io","unitSystem":"metric"}`); code != http.StatusConflict {
		t.Fatalf("email conflict code=%d want %d", code, http.StatusConflict)
	}

	h2 := handlers.NewProfile(&fakeProfileUsers{}, &fakeProfileSessions{}, config.Config{})
	if code := doAuthedPatch(t, h2.Update, `{"displayName":"A","email":"a@x.io","unitSystem":"bogus"}`); code != http.StatusBadRequest {
		t.Fatalf("bad unit code=%d want %d", code, http.StatusBadRequest)
	}
}

func TestProfile_Update_Success(t *testing.T) {
	users := &fakeProfileUsers{user: model.User{ID: 1, Username: testUsername, Email: "a@x.io", DisplayName: "A", UnitSystem: model.UnitMetric}}
	h := handlers.NewProfile(users, &fakeProfileSessions{}, config.Config{})
	if code := doAuthedPatch(t, h.Update, `{"displayName":"A","email":"a@x.io","unitSystem":"metric"}`); code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
}

func TestProfile_Update_RequiresAuth(t *testing.T) {
	h := handlers.NewProfile(&fakeProfileUsers{}, &fakeProfileSessions{}, config.Config{})
	req := httptest.NewRequest(http.MethodPatch, "/api/profile", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestProfile_ChangePassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("current-pw"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	users := &fakeProfileUsers{user: model.User{ID: 1, PasswordHash: string(hash)}}
	sessions := &fakeProfileSessions{}
	h := handlers.NewProfile(users, sessions, config.Config{})

	if code := doAuthedPostWithCookie(t, h.ChangePassword, `{"currentPassword":"WRONG","newPassword":"longenough123","logoutOthers":true}`); code != http.StatusForbidden {
		t.Fatalf("wrong current code=%d want %d", code, http.StatusForbidden)
	}
	if users.passwordUpdated {
		t.Fatal("password updated despite wrong current password")
	}

	body := doAuthedPostBodyWithCookie(t, h.ChangePassword, `{"currentPassword":"current-pw","newPassword":"longenough123","logoutOthers":true}`)
	if !users.passwordUpdated || !sessions.deleteOthersCalled {
		t.Fatalf("expected pw update + revoke-others; users=%+v sessions=%+v", users, sessions)
	}
	if sessions.exceptID != "current-session-id" {
		t.Fatalf("expected DeleteOthersByUser to keep the caller's current session id, got %q", sessions.exceptID)
	}
	if strings.Contains(body, "longenough123") {
		t.Fatalf("new password leaked in response: %s", body)
	}
	if strings.Contains(body, "current-pw") || strings.Contains(body, "WRONG") {
		t.Fatalf("current password leaked in response: %s", body)
	}
	if strings.Contains(body, "passwordHash") || strings.Contains(body, "PasswordHash") {
		t.Fatalf("password hash field leaked in response: %s", body)
	}
}

func TestProfile_ChangePassword_SessionOpError_StillSucceeds(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("current-pw"), bcrypt.DefaultCost)
	users := &fakeProfileUsers{user: model.User{ID: 1, PasswordHash: string(hash)}}
	sessions := &fakeProfileSessions{deleteErr: errors.New("session store unavailable")}
	h := handlers.NewProfile(users, sessions, config.Config{})

	rec := doAuthedPostRaw(t, h.ChangePassword, `{"currentPassword":"current-pw","newPassword":"longenough123","logoutOthers":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (password already changed; session revoke failure should be swallowed)", rec.Code)
	}
	body := rec.Body.String()
	if !users.passwordUpdated {
		t.Fatal("expected password to be updated despite session op error")
	}
	if !sessions.deleteOthersCalled {
		t.Fatalf("expected DeleteOthersByUser to be attempted despite error; sessions=%+v", sessions)
	}
	if strings.Contains(body, "current-pw") || strings.Contains(body, "WRONG") {
		t.Fatalf("current password leaked in response: %s", body)
	}
	if strings.Contains(body, "passwordHash") || strings.Contains(body, "PasswordHash") {
		t.Fatalf("password hash field leaked in response: %s", body)
	}
}

func TestProfile_ChangePassword_NewPasswordTooShort(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("current-pw"), bcrypt.DefaultCost)
	users := &fakeProfileUsers{user: model.User{ID: 1, PasswordHash: string(hash)}}
	h := handlers.NewProfile(users, &fakeProfileSessions{}, config.Config{})

	if code := doAuthedPostWithCookie(t, h.ChangePassword, `{"currentPassword":"current-pw","newPassword":"short","logoutOthers":false}`); code != http.StatusBadRequest {
		t.Fatalf("short new password code=%d want %d", code, http.StatusBadRequest)
	}
	if users.passwordUpdated {
		t.Fatal("password updated despite too-short new password")
	}
}

func TestProfile_ChangePassword_NoLogoutOthers_DoesNotRevoke(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("current-pw"), bcrypt.DefaultCost)
	users := &fakeProfileUsers{user: model.User{ID: 1, PasswordHash: string(hash)}}
	sessions := &fakeProfileSessions{}
	h := handlers.NewProfile(users, sessions, config.Config{})

	if code := doAuthedPostWithCookie(t, h.ChangePassword, `{"currentPassword":"current-pw","newPassword":"longenough123","logoutOthers":false}`); code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
	if !users.passwordUpdated {
		t.Fatal("expected password to be updated")
	}
	if sessions.deleteOthersCalled {
		t.Fatalf("did not expect session revocation when logoutOthers=false; sessions=%+v", sessions)
	}
}

func TestProfile_ChangePassword_LogoutOthers_NoCookie_SkipsRevoke(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("current-pw"), bcrypt.DefaultCost)
	users := &fakeProfileUsers{user: model.User{ID: 1, PasswordHash: string(hash)}}
	sessions := &fakeProfileSessions{}
	h := handlers.NewProfile(users, sessions, config.Config{})

	req := httptest.NewRequest(http.MethodPost, "/api/profile/password", strings.NewReader(
		`{"currentPassword":"current-pw","newPassword":"longenough123","logoutOthers":true}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: testUsername}))
	rec := httptest.NewRecorder()
	h.ChangePassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if !users.passwordUpdated {
		t.Fatal("expected password to be updated")
	}
	if sessions.deleteOthersCalled {
		t.Fatalf("did not expect DeleteOthersByUser without a session cookie; sessions=%+v", sessions)
	}
}

func TestProfile_ChangePassword_RequiresAuth(t *testing.T) {
	h := handlers.NewProfile(&fakeProfileUsers{}, &fakeProfileSessions{}, config.Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/profile/password", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ChangePassword(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestPublicUser_IncludesDisplayNameAndUnitSystem(t *testing.T) {
	h, _ := newAuth(t, "hunter2")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{
		ID: 9, Username: "carol", DisplayName: "Carol", UnitSystem: model.UnitImperial,
	}))
	h.CurrentUser(rec, req)

	var env struct {
		Data struct {
			DisplayName string `json:"displayName"`
			UnitSystem  string `json:"unitSystem"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.DisplayName != "Carol" || env.Data.UnitSystem != model.UnitImperial {
		t.Fatalf("bad publicUser shape: %s", rec.Body.String())
	}
}
