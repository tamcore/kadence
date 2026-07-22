package auth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
)

// BootstrapRepo is the minimal user access BootstrapAdmin needs.
type BootstrapRepo interface {
	Count(ctx context.Context) (int, error)
	Create(ctx context.Context, u model.User) (model.User, error)
}

// BootstrapAdmin creates a single admin user on first run when the users table
// is empty and the KADENCE_ADMIN_* env values are all set. No-op otherwise.
func BootstrapAdmin(ctx context.Context, users BootstrapRepo, cfg config.Config) error {
	if cfg.AdminUsername == "" || cfg.AdminEmail == "" || cfg.AdminPassword == "" {
		return nil
	}
	if len(cfg.AdminPassword) < MinPasswordLen {
		return fmt.Errorf("KADENCE_ADMIN_PASSWORD must be at least %d characters", MinPasswordLen)
	}
	n, err := users.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	if _, err := users.Create(ctx, model.User{
		Username: cfg.AdminUsername, Email: cfg.AdminEmail,
		PasswordHash: hash, Role: model.RoleAdmin,
	}); err != nil {
		return err
	}
	slog.Info("bootstrapped admin user", "username", cfg.AdminUsername)
	return nil
}
