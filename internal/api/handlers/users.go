package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
)

// UsersRepo is the user persistence the admin handler needs.
type UsersRepo interface {
	Create(ctx context.Context, u model.User) (model.User, error)
	ListAll(ctx context.Context) ([]model.User, error)
	Delete(ctx context.Context, id int64) error
	Count(ctx context.Context) (int, error)
}

// Users handles admin user management.
type Users struct{ repo UsersRepo }

// NewUsers constructs the admin Users handler.
func NewUsers(repo UsersRepo) *Users { return &Users{repo: repo} }

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
	if err := h.repo.Delete(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
