// Package config loads server configuration from the environment.
package config

import "os"

// Config holds runtime configuration for the Kadence server.
type Config struct {
	// ListenAddr is the HTTP listen address, e.g. ":8080".
	ListenAddr string
	// Env is the deployment environment: "dev" or "prod".
	Env string
}

const (
	defaultListenAddr = ":8080"
	defaultEnv        = "dev"
)

// Load reads configuration from the environment, applying defaults. It never
// fails in Phase 1: all values have safe defaults.
func Load() Config {
	return Config{
		ListenAddr: envOr("KADENCE_LISTEN_ADDR", defaultListenAddr),
		Env:        envOr("KADENCE_ENV", defaultEnv),
	}
}

// IsProd reports whether the server runs in the production environment.
func (c Config) IsProd() bool { return c.Env == "prod" }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
