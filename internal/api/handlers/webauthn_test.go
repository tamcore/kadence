package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/webauthn"
)

const waCeremonyCookie = "wa_ceremony"

type fakeCreds struct {
	list      []model.WebAuthnCredential
	created   *model.WebAuthnCredential
	renameErr error
	deleteErr error
}

func (f *fakeCreds) Create(_ context.Context, c model.WebAuthnCredential) error {
	f.created = &c
	return nil
}
func (f *fakeCreds) ListByUser(_ context.Context, _ int64) ([]model.WebAuthnCredential, error) {
	return f.list, nil
}
func (f *fakeCreds) GetByCredentialID(_ context.Context, _ []byte) (model.WebAuthnCredential, error) {
	return model.WebAuthnCredential{}, nil
}
func (f *fakeCreds) Rename(_ context.Context, _ string, _ int64, _ string) error { return f.renameErr }
func (f *fakeCreds) DeleteByPublicIDForUser(_ context.Context, _ string, _ int64) error {
	return f.deleteErr
}
func (f *fakeCreds) UpdateSignCount(_ context.Context, _ []byte, _ uint32, _ time.Time) error {
	return nil
}

type fakeWAUsers struct {
	u   model.User
	err error
}

func (f *fakeWAUsers) GetByWebAuthnHandle(_ context.Context, _ string) (model.User, error) {
	return f.u, f.err
}

type fakeSessCreator struct{ created bool }

func (f *fakeSessCreator) Create(_ context.Context, _ model.Session) error {
	f.created = true
	return nil
}

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, _ := crypto.NewCipher(make([]byte, 32))
	return c
}

func enabledCfg() config.Config {
	return config.Config{WebAuthnRPID: "kadence.example.com", TrustedOrigins: []string{"https://kadence.example.com"}}
}

func newWAHandler(t *testing.T, cfg config.Config, creds *fakeCreds, users *fakeWAUsers, sess *fakeSessCreator) *handlers.WebAuthn {
	t.Helper()
	var svc *webauthn.Service
	if cfg.WebAuthnEnabled() {
		s, err := webauthn.NewService(cfg)
		if err != nil {
			t.Fatalf("svc: %v", err)
		}
		svc = s
	}
	return handlers.NewWebAuthn(svc, creds, users, sess, testCipher(t), cfg)
}

func TestWebAuthn_Enabled(t *testing.T) {
	h := newWAHandler(t, enabledCfg(), &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	h.Enabled(rec, httptest.NewRequest(http.MethodGet, "/api/webauthn/enabled", nil))
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestWebAuthn_Disabled_RegisterBegin404(t *testing.T) {
	h := newWAHandler(t, config.Config{}, &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/begin", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: "u"}))
	h.RegisterBegin(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 when disabled", rec.Code)
	}
}

func TestWebAuthn_RegisterBegin_SetsCeremonyCookie(t *testing.T) {
	h := newWAHandler(t, enabledCfg(), &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/begin", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: testUsername, WebAuthnHandle: "h-1"}))
	h.RegisterBegin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == waCeremonyCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("wa_ceremony cookie not set")
	}
	if !strings.Contains(rec.Body.String(), "publicKey") {
		t.Fatalf("expected creation options, got %s", rec.Body.String())
	}
}

func TestWebAuthn_RegisterFinish_TamperedCeremony400(t *testing.T) {
	h := newWAHandler(t, enabledCfg(), &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/finish?name=x", strings.NewReader("{}"))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: 1, Username: testUsername, WebAuthnHandle: "h-1"}))
	req.AddCookie(&http.Cookie{Name: waCeremonyCookie, Value: "garbage"})
	h.RegisterFinish(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for tampered ceremony", rec.Code)
	}
}

func TestWebAuthn_LoginBegin_SetsCeremonyAndOptions(t *testing.T) {
	h := newWAHandler(t, enabledCfg(), &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	h.LoginBegin(rec, httptest.NewRequest(http.MethodPost, "/api/webauthn/login/begin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == waCeremonyCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("wa_ceremony cookie not set on login begin")
	}
	if !strings.Contains(rec.Body.String(), "publicKey") {
		t.Fatalf("expected assertion options, got %s", rec.Body.String())
	}
}

func TestWebAuthn_LoginFinish_TamperedCeremony400(t *testing.T) {
	h := newWAHandler(t, enabledCfg(), &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: waCeremonyCookie, Value: "garbage"})
	h.LoginFinish(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestWebAuthn_LoginFinish_Disabled404(t *testing.T) {
	h := newWAHandler(t, config.Config{}, &fakeCreds{}, &fakeWAUsers{}, &fakeSessCreator{})
	rec := httptest.NewRecorder()
	h.LoginFinish(rec, httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", strings.NewReader("{}")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}
