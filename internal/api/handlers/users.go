package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

// UsersRepo is the user persistence the admin handler needs.
type UsersRepo interface {
	Create(ctx context.Context, u model.User) (model.User, error)
	ListAll(ctx context.Context) ([]model.User, error)
	Delete(ctx context.Context, id int64) error
	Count(ctx context.Context) (int, error)
	GetByID(ctx context.Context, id int64) (model.User, error)
	UpdateUser(ctx context.Context, id int64, username, email, role string) (model.User, error)
	UpdatePassword(ctx context.Context, id int64, passwordHash string) error
	CountAdmins(ctx context.Context) (int, error)
}

// UsersSessions is the session revocation the admin handler needs when a
// password reset should invalidate existing logins.
type UsersSessions interface {
	DeleteAllByUser(ctx context.Context, userID int64) error
	DeleteOthersByUser(ctx context.Context, userID int64, exceptID string) error
}

// Users handles admin user management.
type Users struct {
	repo     UsersRepo
	sessions UsersSessions
}

// NewUsers constructs the admin Users handler.
func NewUsers(repo UsersRepo, sessions UsersSessions) *Users {
	return &Users{repo: repo, sessions: sessions}
}

// List returns all users.
func (h *Users) List(w http.ResponseWriter, r *http.Request) {
	all, err := h.repo.ListAll(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list users")
		return
	}
	out := make([]publicUser, 0, len(all))
	for _, u := range all {
		out = append(out, toPublic(u))
	}
	RespondJSON(w, http.StatusOK, out)
}

// Create creates a user (admin only). Password is hashed.
func (h *Users) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Username == "" || body.Email == "" || body.Password == "" {
		RespondError(w, http.StatusBadRequest, "username, email, and password are required")
		return
	}
	if len(body.Password) < auth.MinPasswordLen {
		RespondError(w, http.StatusBadRequest, "password is too short")
		return
	}
	if body.Role != model.RoleAdmin && body.Role != model.RoleUser {
		RespondError(w, http.StatusBadRequest, "role must be 'admin' or 'user'")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	created, err := h.repo.Create(r.Context(), model.User{
		Username: body.Username, Email: body.Email, PasswordHash: hash, Role: body.Role,
	})
	if err != nil {
		RespondError(w, http.StatusConflict, "could not create user (username or email may already exist)")
		return
	}
	RespondJSON(w, http.StatusCreated, toPublic(created))
}

// Delete removes a user by id.
func (h *Users) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// Guard against deleting the last remaining admin, which would leave nobody
	// able to administer the instance. Mirrors the demotion guard in Update.
	target, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			RespondError(w, http.StatusNotFound, "user not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not load user")
		return
	}
	if target.IsAdmin() {
		admins, err := h.repo.CountAdmins(r.Context())
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "could not verify admin count")
			return
		}
		if admins <= 1 {
			RespondError(w, http.StatusBadRequest, "cannot delete the last remaining admin")
			return
		}
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Update edits an existing user's username, email, role, and optionally their
// password (admin only). A blank password leaves the existing one unchanged.
func (h *Users) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Username == "" || body.Email == "" {
		RespondError(w, http.StatusBadRequest, "username and email are required")
		return
	}
	if body.Role != model.RoleAdmin && body.Role != model.RoleUser {
		RespondError(w, http.StatusBadRequest, "role must be 'admin' or 'user'")
		return
	}

	current, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			RespondError(w, http.StatusNotFound, "user not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not load user")
		return
	}

	// Guard against demoting the last remaining admin, which would leave nobody
	// able to administer the instance.
	if current.IsAdmin() && body.Role != model.RoleAdmin {
		admins, err := h.repo.CountAdmins(r.Context())
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "could not verify admin count")
			return
		}
		if admins <= 1 {
			RespondError(w, http.StatusBadRequest, "cannot demote the last remaining admin")
			return
		}
	}

	// Hash the new password (if any) up front, but apply the core field update
	// first: it carries the unique-constraint checks and is the likeliest to
	// fail. Only touch the password once the rest of the edit has succeeded, so
	// an error response never leaves a silently-changed password behind.
	var newHash string
	if body.Password != "" {
		if len(body.Password) < auth.MinPasswordLen {
			RespondError(w, http.StatusBadRequest, "password is too short")
			return
		}
		h, err := auth.HashPassword(body.Password)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "could not hash password")
			return
		}
		newHash = h
	}

	updated, err := h.repo.UpdateUser(r.Context(), id, body.Username, body.Email, body.Role)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrUsernameTaken):
			RespondError(w, http.StatusBadRequest, "username already in use")
		case errors.Is(err, store.ErrEmailTaken):
			RespondError(w, http.StatusBadRequest, "email already in use")
		case errors.Is(err, store.ErrNotFound):
			RespondError(w, http.StatusNotFound, "user not found")
		default:
			RespondError(w, http.StatusInternalServerError, "could not update user")
		}
		return
	}

	if newHash != "" {
		if err := h.repo.UpdatePassword(r.Context(), id, newHash); err != nil {
			RespondError(w, http.StatusInternalServerError, "could not update password")
			return
		}
		h.revokeAfterPasswordReset(r, id)
	}
	RespondJSON(w, http.StatusOK, toPublic(updated))
}

// revokeAfterPasswordReset invalidates existing logins for the target of an
// admin password reset: a compromised account must not stay logged in past
// the reset. When the acting admin resets their own password, their current
// session is preserved (DeleteOthersByUser) so they are not logged out by
// their own action, mirroring Profile.ChangePassword's logoutOthers
// semantics; resetting anyone else's password revokes all of the target's
// sessions (DeleteAllByUser). Failures are logged and swallowed: the
// password change itself already succeeded, so a 500 here would be
// misleading.
func (h *Users) revokeAfterPasswordReset(r *http.Request, targetID int64) {
	actor := auth.UserFromContext(r.Context())
	if actor != nil && actor.ID == targetID {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			slog.Warn("admin self password reset without a current session cookie; revoking all sessions", "user_id", targetID)
			if err := h.sessions.DeleteAllByUser(r.Context(), targetID); err != nil {
				slog.Error("revoke sessions after admin password reset", "user_id", targetID, "err", err)
			}
			return
		}
		if err := h.sessions.DeleteOthersByUser(r.Context(), targetID, c.Value); err != nil {
			slog.Error("revoke other sessions after admin self password reset", "user_id", targetID, "err", err)
		}
		return
	}
	if err := h.sessions.DeleteAllByUser(r.Context(), targetID); err != nil {
		slog.Error("revoke sessions after admin password reset", "user_id", targetID, "err", err)
	}
}
