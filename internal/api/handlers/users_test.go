package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
)

type usersRepo struct {
	list    []model.User
	created *model.User
	deleted int64
}

func (r *usersRepo) Create(_ context.Context, u model.User) (model.User, error) {
	u.ID = 10
	r.created = &u
	return u, nil
}
func (r *usersRepo) ListAll(context.Context) ([]model.User, error) { return r.list, nil }
func (r *usersRepo) Delete(_ context.Context, id int64) error      { r.deleted = id; return nil }
func (r *usersRepo) Count(context.Context) (int, error)            { return len(r.list), nil }

func TestCreateUserHashesAndInserts(t *testing.T) {
	repo := &usersRepo{}
	h := handlers.NewUsers(repo)
	body := `{"username":"bob","email":"b@x.io","password":"pw","role":"user"}`
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.PasswordHash == "pw" || repo.created.Role != "user" {
		t.Fatalf("bad create: %+v", repo.created)
	}
}

func TestCreateUserRejectsBadRole(t *testing.T) {
	h := handlers.NewUsers(&usersRepo{})
	body := `{"username":"bob","email":"b@x.io","password":"pw","role":"superadmin"}`
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDeleteUserParsesID(t *testing.T) {
	repo := &usersRepo{}
	h := handlers.NewUsers(repo)
	r := chi.NewRouter()
	r.Delete("/api/users/{id}", h.Delete)
	srv := httptest.NewServer(r)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/users/42", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || repo.deleted != 42 {
		t.Fatalf("status=%d deleted=%d", resp.StatusCode, repo.deleted)
	}
}
