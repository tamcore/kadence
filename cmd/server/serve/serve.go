// Package serve runs the Kadence HTTP server with graceful shutdown.
package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
	startupTimeout    = 30 * time.Second
)

// Run starts the HTTP server and blocks until SIGINT/SIGTERM.
func Run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	startupCtx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	pool, err := store.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close(pool)

	if err := store.Migrate(startupCtx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	if err := auth.BootstrapAdmin(startupCtx, users, cfg); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.NewRouter(api.Deps{Users: users, Sessions: sessions, Config: cfg}),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", cfg.ListenAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		slog.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel2()
	return srv.Shutdown(shutdownCtx)
}
