package auth_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
)

const testAdminUsername = "admin"

type bootRepo struct {
	count   int
	created *model.User
}

func (r *bootRepo) Count(context.Context) (int, error) { return r.count, nil }
func (r *bootRepo) Create(_ context.Context, u model.User) (model.User, error) {
	u.ID = 1
	r.created = &u
	return u, nil
}

func TestBootstrapCreatesAdminWhenEmpty(t *testing.T) {
	r := &bootRepo{count: 0}
	cfg := config.Config{AdminUsername: testAdminUsername, AdminEmail: "a@x.io", AdminPassword: "pw"}
	if err := auth.BootstrapAdmin(context.Background(), r, cfg); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	if r.created == nil || r.created.Role != model.RoleAdmin || r.created.Username != testAdminUsername {
		t.Fatalf("admin not created: %+v", r.created)
	}
	if r.created.PasswordHash == "pw" || r.created.PasswordHash == "" {
		t.Fatal("password not hashed")
	}
}

func TestBootstrapNoOpWhenUsersExist(t *testing.T) {
	r := &bootRepo{count: 3}
	cfg := config.Config{AdminUsername: testAdminUsername, AdminEmail: "a@x.io", AdminPassword: "pw"}
	_ = auth.BootstrapAdmin(context.Background(), r, cfg)
	if r.created != nil {
		t.Fatal("should not create admin when users already exist")
	}
}

func TestBootstrapNoOpWhenUnconfigured(t *testing.T) {
	r := &bootRepo{count: 0}
	_ = auth.BootstrapAdmin(context.Background(), r, config.Config{})
	if r.created != nil {
		t.Fatal("should not create admin without env config")
	}
}
