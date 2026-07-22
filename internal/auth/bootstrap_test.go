package auth_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
)

const (
	testAdminUsername = "admin"
	testAdminEmail    = "a@x.io"
	testAdminPassword = "longenoughpw"
)

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
	cfg := config.Config{AdminUsername: testAdminUsername, AdminEmail: testAdminEmail, AdminPassword: testAdminPassword}
	if err := auth.BootstrapAdmin(context.Background(), r, cfg); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	if r.created == nil || r.created.Role != model.RoleAdmin || r.created.Username != testAdminUsername {
		t.Fatalf("admin not created: %+v", r.created)
	}
	if r.created.PasswordHash == testAdminPassword || r.created.PasswordHash == "" {
		t.Fatal("password not hashed")
	}
}

func TestBootstrapNoOpWhenUsersExist(t *testing.T) {
	r := &bootRepo{count: 3}
	cfg := config.Config{AdminUsername: testAdminUsername, AdminEmail: testAdminEmail, AdminPassword: testAdminPassword}
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

func TestBootstrapFailsOnShortPassword(t *testing.T) {
	r := &bootRepo{count: 0}
	cfg := config.Config{AdminUsername: testAdminUsername, AdminEmail: testAdminEmail, AdminPassword: "short7c"}
	err := auth.BootstrapAdmin(context.Background(), r, cfg)
	if err == nil {
		t.Fatal("expected an error for a too-short KADENCE_ADMIN_PASSWORD, got nil")
	}
	if r.created != nil {
		t.Fatalf("admin should not have been created on short password, got %+v", r.created)
	}
}
