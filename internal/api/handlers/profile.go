package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

// minPasswordLen is the minimum acceptable length for a new account password.
const minPasswordLen = 8

// profileUsers is the user persistence the profile handler needs.
type profileUsers interface {
	GetByID(ctx context.Context, id int64) (model.User, error)
	UpdateProfile(ctx context.Context, id int64, displayName, email, unitSystem string) error
	UpdatePassword(ctx context.Context, id int64, passwordHash string) error
}

// profileSessions is the session persistence the profile handler needs.
type profileSessions interface {
	GetByID(ctx context.Context, id string) (model.Session, error)
	DeleteAllByUser(ctx context.Context, userID int64) error
	Create(ctx context.Context, s model.Session) error
}

// Profile handles authenticated self-service profile and password updates.
type Profile struct {
	users    profileUsers
	sessions profileSessions
	cfg      config.Config
}

// NewProfile constructs a Profile handler.
func NewProfile(users profileUsers, sessions profileSessions, cfg config.Config) *Profile {
	return &Profile{users: users, sessions: sessions, cfg: cfg}
}

// Update handles PATCH /api/profile: display name, email, and unit system
// preference for the authenticated user. The target user is always taken
// from the request context, never from the body, to prevent updating other
// accounts.
func (h *Profile) Update(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var in struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
		UnitSystem  string `json:"unitSystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if in.UnitSystem != model.UnitMetric && in.UnitSystem != model.UnitImperial {
		RespondError(w, http.StatusBadRequest, "unitSystem must be metric or imperial")
		return
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || !strings.Contains(email, "@") {
		RespondError(w, http.StatusBadRequest, "valid email required")
		return
	}

	if err := h.users.UpdateProfile(r.Context(), u.ID, strings.TrimSpace(in.DisplayName), email, in.UnitSystem); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			RespondError(w, http.StatusConflict, "that email is already in use")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not update profile")
		return
	}

	updated, err := h.users.GetByID(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load profile")
		return
	}
	RespondJSON(w, http.StatusOK, toPublic(updated))
}

// ChangePassword handles POST /api/profile/password. The current password
// must be verified before the new one is set; on success, other sessions are
// optionally revoked while the current session is preserved.
func (h *Profile) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		LogoutOthers    bool   `json:"logoutOthers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(in.NewPassword) < minPasswordLen {
		RespondError(w, http.StatusBadRequest, "new password is too short")
		return
	}

	full, err := h.users.GetByID(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load account")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(full.PasswordHash), []byte(in.CurrentPassword)) != nil {
		RespondError(w, http.StatusForbidden, "current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not set new password")
		return
	}
	if err := h.users.UpdatePassword(r.Context(), u.ID, newHash); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not set new password")
		return
	}

	if in.LogoutOthers {
		h.revokeOthersKeepCurrent(w, r, u.ID)
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// revokeOthersKeepCurrent deletes all sessions for userID and issues a fresh
// session cookie for the current browser so the caller is not logged out by
// their own password change. The current session's RememberMe flag is
// preserved. Failures here are logged and swallowed: the password change
// itself already succeeded by the time this runs, so a 500 response would be
// misleading.
func (h *Profile) revokeOthersKeepCurrent(w http.ResponseWriter, r *http.Request, userID int64) {
	ctx := r.Context()

	var rememberMe bool
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if cur, gErr := h.sessions.GetByID(ctx, c.Value); gErr == nil {
			rememberMe = cur.RememberMe
		}
	}

	if err := h.sessions.DeleteAllByUser(ctx, userID); err != nil {
		slog.Error("revoke other sessions after password change", "user_id", userID, "err", err)
		return
	}

	newID, err := auth.NewSessionID()
	if err != nil {
		slog.Error("generate replacement session after password change", "user_id", userID, "err", err)
		return
	}
	ttl := defaultTTL
	if rememberMe {
		ttl = rememberTTL
	}
	expiresAt := time.Now().Add(ttl)
	if err := h.sessions.Create(ctx, model.Session{
		ID: newID, UserID: userID, RememberMe: rememberMe, ExpiresAt: expiresAt,
	}); err != nil {
		slog.Error("create replacement session after password change", "user_id", userID, "err", err)
		return
	}

	setSessionCookie(w, h.cfg, newID, expiresAt)
}
