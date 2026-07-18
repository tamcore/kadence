// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config holds runtime configuration for the Kadence server.
type Config struct {
	ListenAddr string
	Env        string

	// DatabaseURL is the Postgres DSN (KADENCE_DATABASE_URL).
	DatabaseURL string
	// CSRFSecret is the gorilla/csrf secret (KADENCE_CSRF_SECRET).
	CSRFSecret string

	// Admin bootstrap (used once, on first run, when the users table is empty).
	AdminUsername string
	AdminEmail    string
	AdminPassword string
}

const (
	defaultListenAddr = ":8080"
	defaultEnv        = "dev"
)

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		ListenAddr:    envOr("KADENCE_LISTEN_ADDR", defaultListenAddr),
		Env:           envOr("KADENCE_ENV", defaultEnv),
		DatabaseURL:   os.Getenv("KADENCE_DATABASE_URL"),
		CSRFSecret:    os.Getenv("KADENCE_CSRF_SECRET"),
		AdminUsername: os.Getenv("KADENCE_ADMIN_USERNAME"),
		AdminEmail:    os.Getenv("KADENCE_ADMIN_EMAIL"),
		AdminPassword: os.Getenv("KADENCE_ADMIN_PASSWORD"),
	}
}

// IsProd reports whether the server runs in the production environment.
func (c Config) IsProd() bool { return c.Env == "prod" }

// Validate checks required runtime configuration. Call before starting the server.
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("KADENCE_DATABASE_URL is required")
	}
	if c.IsProd() && c.CSRFSecret == "" {
		return errors.New("KADENCE_CSRF_SECRET is required in production")
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
