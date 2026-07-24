package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// profileUsers is the user persistence the profile handler needs.
type profileUsers interface {
	GetByID(ctx context.Context, id int64) (model.User, error)
	UpdateProfile(ctx context.Context, id int64, displayName, email, unitSystem, location, aboutMe string) error
	UpdateTimezone(ctx context.Context, id int64, timezone string) error
	UpdatePassword(ctx context.Context, id int64, passwordHash string) error
}

// Field length caps enforced on PATCH /api/profile.
const (
	maxLocationLen = 120
	maxAboutMeLen  = 1000
)

// profileSessions is the session persistence the profile handler needs.
type profileSessions interface {
	DeleteOthersByUser(ctx context.Context, userID int64, exceptID string) error
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

	var in struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
		UnitSystem  string `json:"unitSystem"`
		Location    string `json:"location"`
		AboutMe     string `json:"aboutMe"`
		Timezone    string `json:"timezone"`
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
	location := strings.TrimSpace(in.Location)
	if len(location) > maxLocationLen {
		RespondError(w, http.StatusBadRequest, fmt.Sprintf("location must be %d characters or fewer", maxLocationLen))
		return
	}
	aboutMe := strings.TrimSpace(in.AboutMe)
	if len(aboutMe) > maxAboutMeLen {
		RespondError(w, http.StatusBadRequest, fmt.Sprintf("about me must be %d characters or fewer", maxAboutMeLen))
		return
	}
	timezone := strings.TrimSpace(in.Timezone)
	if timezone == "" {
		timezone = u.Timezone
		if timezone == "" {
			timezone = "UTC"
		}
	}
	if _, err := time.LoadLocation(timezone); err != nil || (timezone != "UTC" && !strings.Contains(timezone, "/")) {
		RespondError(w, http.StatusBadRequest, "timezone must be an IANA timezone")
		return
	}

	if err := h.users.UpdateProfile(r.Context(), u.ID, strings.TrimSpace(in.DisplayName), email, in.UnitSystem, location, aboutMe); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			RespondError(w, http.StatusConflict, "that email is already in use")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not update profile")
		return
	}
	if err := h.users.UpdateTimezone(r.Context(), u.ID, timezone); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not update profile")
		return
	}

	updated, err := h.users.GetByID(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load profile")
		return
	}
	RespondJSON(w, http.StatusOK, toPublicWithConfig(updated, h.cfg))
}

// ChangePassword handles POST /api/profile/password. The current password
// must be verified before the new one is set; on success, other sessions are
// optionally revoked while the current session is preserved.
func (h *Profile) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())

	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		LogoutOthers    bool   `json:"logoutOthers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(in.NewPassword) < auth.MinPasswordLen {
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
		h.revokeOthersKeepCurrent(r, u.ID)
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// revokeOthersKeepCurrent deletes every session for userID except the
// caller's current one, identified by the session cookie on r. The current
// session is left completely untouched: no recreate, no cookie rotation, so
// there is no window where the caller could be logged out by their own
// password change. Failures here are logged and swallowed: the password
// change itself already succeeded by the time this runs, so a 500 response
// would be misleading.
func (h *Profile) revokeOthersKeepCurrent(r *http.Request, userID int64) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		slog.Warn("logout-others requested without a current session cookie", "user_id", userID)
		return
	}

	if err := h.sessions.DeleteOthersByUser(r.Context(), userID, c.Value); err != nil {
		slog.Error("revoke other sessions after password change", "user_id", userID, "err", err)
	}
}
