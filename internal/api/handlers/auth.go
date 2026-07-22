// Package handlers holds Kadence HTTP handlers.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
)

const (
	sessionCookie = "session_id"
	defaultTTL    = 24 * time.Hour
	rememberTTL   = 30 * 24 * time.Hour
)

// AuthUsers is the user lookup the auth handler needs.
type AuthUsers interface {
	GetByUsername(ctx context.Context, username string) (model.User, error)
}

// AuthSessions is the session persistence the auth handler needs.
type AuthSessions interface {
	Create(ctx context.Context, s model.Session) error
	Delete(ctx context.Context, id string) error
}

// Auth handles login, logout, and current-user.
type Auth struct {
	cfg      config.Config
	users    AuthUsers
	sessions AuthSessions
}

// NewAuth constructs an Auth handler.
func NewAuth(cfg config.Config, users AuthUsers, sessions AuthSessions) *Auth {
	return &Auth{cfg: cfg, users: users, sessions: sessions}
}

type publicUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
	UnitSystem  string `json:"unitSystem"`
}

func toPublic(u model.User) publicUser {
	return publicUser{
		ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role,
		DisplayName: u.DisplayName, UnitSystem: u.UnitSystem,
	}
}

// Login authenticates by username + password and starts a session.
func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	u, err := h.users.GetByUsername(r.Context(), body.Username)
	if err != nil {
		// Unknown username: still pay the bcrypt cost so this path takes the
		// same time as a wrong-password attempt against a real account,
		// closing the username-enumeration timing oracle.
		auth.CheckPasswordDummy(body.Password)
		RespondError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, body.Password) {
		RespondError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	pub, err := startSession(w, r, h.cfg, h.sessions, u, body.Remember)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	RespondJSON(w, http.StatusOK, pub)
}

// sessionCreator is the minimal session persistence needed to start a session.
type sessionCreator interface {
	Create(ctx context.Context, s model.Session) error
}

// startSession mints a session id, persists the session (capturing UA + IP),
// sets the session cookie, and returns the public user view. Shared by
// password login and passkey login.
func startSession(w http.ResponseWriter, r *http.Request, cfg config.Config, sessions sessionCreator, u model.User, remember bool) (publicUser, error) {
	id, err := auth.NewSessionID()
	if err != nil {
		return publicUser{}, err
	}
	ttl := defaultTTL
	if remember {
		ttl = rememberTTL
	}
	expiresAt := time.Now().Add(ttl)
	if err := sessions.Create(r.Context(), model.Session{
		ID: id, UserID: u.ID, RememberMe: remember, ExpiresAt: expiresAt,
		UserAgent: r.UserAgent(), IP: auth.ClientIP(r),
	}); err != nil {
		return publicUser{}, err
	}
	setSessionCookie(w, cfg, id, expiresAt)
	return toPublic(u), nil
}

// setSessionCookie writes the session cookie with the exact name and
// attributes used across all session-issuing handlers (login, and profile
// password-change session rotation).
func setSessionCookie(w http.ResponseWriter, cfg config.Config, id string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/",
		Expires: expiresAt, MaxAge: int(time.Until(expiresAt).Seconds()),
		HttpOnly: true, Secure: cfg.IsProd(), SameSite: http.SameSiteLaxMode,
	})
}

// Logout deletes the current session and clears the cookie.
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = h.sessions.Delete(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		Expires: time.Unix(0, 0), MaxAge: -1,
		HttpOnly: true, Secure: h.cfg.IsProd(), SameSite: http.SameSiteLaxMode,
	})
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// CurrentUser returns the authenticated user from context.
func (h *Auth) CurrentUser(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	RespondJSON(w, http.StatusOK, toPublic(*u))
}
