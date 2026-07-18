// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
	"strconv"
	"time"
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

	// LLM (OpenAI-compatible) provider config. Chat is enabled iff LLMAPIKey != "".
	LLMBaseURL     string
	LLMAPIKey      string
	LLMModel       string
	LLMMaxTokens   int
	LLMTemperature float64
	LLMTimeout     time.Duration
	// SystemPrompt overrides the default chat system prompt.
	SystemPrompt string
}

const (
	defaultListenAddr = ":8080"
	defaultEnv        = "dev"
)

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	cfg := Config{
		ListenAddr:    envOr("KADENCE_LISTEN_ADDR", defaultListenAddr),
		Env:           envOr("KADENCE_ENV", defaultEnv),
		DatabaseURL:   os.Getenv("KADENCE_DATABASE_URL"),
		CSRFSecret:    os.Getenv("KADENCE_CSRF_SECRET"),
		AdminUsername: os.Getenv("KADENCE_ADMIN_USERNAME"),
		AdminEmail:    os.Getenv("KADENCE_ADMIN_EMAIL"),
		AdminPassword: os.Getenv("KADENCE_ADMIN_PASSWORD"),
	}

	cfg.LLMBaseURL = envOr("KADENCE_LLM_BASE_URL", "https://api.openai.com/v1")
	cfg.LLMAPIKey = os.Getenv("KADENCE_LLM_API_KEY")
	cfg.LLMModel = envOr("KADENCE_LLM_MODEL", "gpt-4o-mini")
	cfg.LLMMaxTokens = envIntOr("KADENCE_LLM_MAX_TOKENS", 2048)
	cfg.LLMTemperature = envFloatOr("KADENCE_LLM_TEMPERATURE", 0.3)
	cfg.LLMTimeout = envDurationOr("KADENCE_LLM_TIMEOUT", 90*time.Second)
	cfg.SystemPrompt = os.Getenv("KADENCE_SYSTEM_PROMPT")

	return cfg
}

// IsProd reports whether the server runs in the production environment.
func (c Config) IsProd() bool { return c.Env == "prod" }

// ChatEnabled reports whether LLM chat is configured.
func (c Config) ChatEnabled() bool { return c.LLMAPIKey != "" }

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

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envFloatOr(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
