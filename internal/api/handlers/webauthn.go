package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/webauthn"
)

const defaultPasskeyName = "Passkey"

type webauthnCreds interface {
	Create(ctx context.Context, c model.WebAuthnCredential) error
	ListByUser(ctx context.Context, userID int64) ([]model.WebAuthnCredential, error)
	GetByCredentialID(ctx context.Context, credID []byte) (model.WebAuthnCredential, error)
	Rename(ctx context.Context, publicID string, userID int64, name string) error
	DeleteByPublicIDForUser(ctx context.Context, publicID string, userID int64) error
	UpdateSignCount(ctx context.Context, credID []byte, signCount uint32, lastUsed time.Time) error
}

type webauthnUsers interface {
	GetByWebAuthnHandle(ctx context.Context, handle string) (model.User, error)
}

// WebAuthn serves passkey registration, login, and management.
type WebAuthn struct {
	svc      *webauthn.Service
	creds    webauthnCreds
	users    webauthnUsers
	sessions sessionCreator
	cipher   *crypto.Cipher
	cfg      config.Config
}

// NewWebAuthn builds the handler. svc may be nil when the feature is disabled;
// handlers guard on cfg.WebAuthnEnabled() before touching svc.
func NewWebAuthn(svc *webauthn.Service, creds webauthnCreds, users webauthnUsers, sessions sessionCreator, cipher *crypto.Cipher, cfg config.Config) *WebAuthn {
	return &WebAuthn{svc: svc, creds: creds, users: users, sessions: sessions, cipher: cipher, cfg: cfg}
}

func (h *WebAuthn) disabled(w http.ResponseWriter) bool {
	if !h.cfg.WebAuthnEnabled() {
		RespondError(w, http.StatusNotFound, "not found")
		return true
	}
	return false
}

// Enabled reports whether passkeys are configured (always 200, no auth).
func (h *WebAuthn) Enabled(w http.ResponseWriter, _ *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]bool{"enabled": h.cfg.WebAuthnEnabled()})
}

func (h *WebAuthn) userAdapter(ctx context.Context, u model.User) (webauthn.User, error) {
	stored, err := h.creds.ListByUser(ctx, u.ID)
	if err != nil {
		return webauthn.User{}, err
	}
	gcreds := make([]gwa.Credential, len(stored))
	for i, c := range stored {
		gcreds[i] = webauthn.ToCredential(c)
	}
	return webauthn.User{Handle: u.WebAuthnHandle, Username: u.Username, Display: u.DisplayName, Creds: gcreds}, nil
}

// RegisterBegin starts a passkey registration for the authenticated user.
func (h *WebAuthn) RegisterBegin(w http.ResponseWriter, r *http.Request) {
	if h.disabled(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	waUser, err := h.userAdapter(r.Context(), *u)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load credentials")
		return
	}
	options, sess, err := h.svc.BeginRegistration(waUser)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not begin registration")
		return
	}
	if err := webauthn.WriteCeremony(w, h.cfg, h.cipher, sess); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not start ceremony")
		return
	}
	RespondJSON(w, http.StatusOK, options)
}

// RegisterFinish verifies the attestation and stores the credential. The
// passkey name is passed as the ?name= query parameter (the request body is
// the WebAuthn attestation JSON consumed by the library).
func (h *WebAuthn) RegisterFinish(w http.ResponseWriter, r *http.Request) {
	if h.disabled(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	sess, err := webauthn.ReadCeremony(r, h.cipher)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "ceremony expired or invalid")
		return
	}
	waUser, err := h.userAdapter(r.Context(), *u)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load credentials")
		return
	}
	cred, err := h.svc.FinishRegistration(waUser, sess, r)
	if err != nil {
		webauthn.ClearCeremony(w, h.cfg)
		RespondError(w, http.StatusBadRequest, "could not verify passkey")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = defaultPasskeyName
	}
	if err := h.creds.Create(r.Context(), webauthn.FromCredential(cred, u.ID, name)); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not store passkey")
		return
	}
	webauthn.ClearCeremony(w, h.cfg)
	RespondJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

// LoginBegin starts a usernameless (discoverable) passkey assertion.
func (h *WebAuthn) LoginBegin(w http.ResponseWriter, r *http.Request) {
	if h.disabled(w) {
		return
	}
	options, sess, err := h.svc.BeginDiscoverableLogin()
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not begin login")
		return
	}
	if err := webauthn.WriteCeremony(w, h.cfg, h.cipher, sess); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not start ceremony")
		return
	}
	RespondJSON(w, http.StatusOK, options)
}

// LoginFinish verifies the assertion, resolves the user by handle, bumps the
// sign counter, and starts a session (same path as password login).
func (h *WebAuthn) LoginFinish(w http.ResponseWriter, r *http.Request) {
	if h.disabled(w) {
		return
	}
	sess, err := webauthn.ReadCeremony(r, h.cipher)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "ceremony expired or invalid")
		return
	}

	var matched model.User
	handler := func(_, userHandle []byte) (gwa.User, error) {
		u, err := h.users.GetByWebAuthnHandle(r.Context(), string(userHandle))
		if err != nil {
			return nil, err
		}
		waUser, err := h.userAdapter(r.Context(), u)
		if err != nil {
			return nil, err
		}
		matched = u
		return waUser, nil
	}

	cred, err := h.svc.FinishDiscoverableLogin(handler, sess, r)
	if err != nil {
		webauthn.ClearCeremony(w, h.cfg)
		RespondError(w, http.StatusUnauthorized, "could not verify passkey")
		return
	}
	if cred.Authenticator.CloneWarning {
		webauthn.ClearCeremony(w, h.cfg)
		RespondError(w, http.StatusUnauthorized, "could not verify passkey")
		return
	}
	_ = h.creds.UpdateSignCount(r.Context(), cred.ID, cred.Authenticator.SignCount, time.Now())
	webauthn.ClearCeremony(w, h.cfg)

	pub, err := startSession(w, r, h.cfg, h.sessions, matched, false)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	RespondJSON(w, http.StatusOK, pub)
}
