// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
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
	// TrustedOrigins are CSRF-trusted origins (KADENCE_TRUSTED_ORIGINS, comma-split, trimmed).
	TrustedOrigins []string

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

	// Guardrail (opt-in topic classifier). Model/base/key fall back to the LLM* values.
	GuardrailEnabled       bool
	GuardrailModel         string
	GuardrailBaseURL       string
	GuardrailAPIKey        string
	GuardrailHistoryWindow int
	// Domain config for the guardrail classifier prompt + refusal.
	DomainName     string
	AllowedTopics  string
	RefusalMessage string

	// Embeddings / RAG. RAG is enabled iff EmbedAPIKey != "".
	EmbedBaseURL string
	EmbedAPIKey  string
	EmbedModel   string
	RAGTopK      int

	// Ingestion.
	UploadMaxBytes   int
	IngestChunkChars int

	// MCP orchestration.
	MCPMaxIterations int
	// MCPMaxTools caps the number of MCP tool definitions injected into a
	// single chat request, as defense-in-depth against provider tool-count
	// limits (e.g. OpenAI's 128-tool cap) when many MCP servers are configured.
	MCPMaxTools int
}

const (
	defaultListenAddr = ":8080"
	defaultEnv        = "dev"
	envProd           = "prod"
	envProduction     = "production"

	defaultDomainName    = "Kadence, an endurance-sports coaching assistant"
	defaultAllowedTopics = "endurance training and racing (running, cycling, swimming, triathlon), " +
		"workouts, training plans, pacing, recovery, injury-prevention basics, sports nutrition and hydration, " +
		"race preparation, and the user's own training data and progress"
	defaultRefusalMessage = "I'm your coaching assistant, so I can only help with training, workouts, " +
		"recovery, nutrition, and race prep. What would you like to work on?"
)

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	cfg := Config{
		ListenAddr:     envOr("KADENCE_LISTEN_ADDR", defaultListenAddr),
		Env:            envOr("KADENCE_ENV", defaultEnv),
		DatabaseURL:    os.Getenv("KADENCE_DATABASE_URL"),
		CSRFSecret:     os.Getenv("KADENCE_CSRF_SECRET"),
		TrustedOrigins: loadTrustedOrigins(os.Getenv("KADENCE_TRUSTED_ORIGINS")),
		AdminUsername:  os.Getenv("KADENCE_ADMIN_USERNAME"),
		AdminEmail:     os.Getenv("KADENCE_ADMIN_EMAIL"),
		AdminPassword:  os.Getenv("KADENCE_ADMIN_PASSWORD"),
	}

	cfg.LLMBaseURL = envOr("KADENCE_LLM_BASE_URL", "https://api.openai.com/v1")
	cfg.LLMAPIKey = os.Getenv("KADENCE_LLM_API_KEY")
	cfg.LLMModel = envOr("KADENCE_LLM_MODEL", "gpt-4o-mini")
	cfg.LLMMaxTokens = envIntOr("KADENCE_LLM_MAX_TOKENS", 2048)
	cfg.LLMTemperature = envFloatOr("KADENCE_LLM_TEMPERATURE", 0.3)
	cfg.LLMTimeout = envDurationOr("KADENCE_LLM_TIMEOUT", 90*time.Second)
	cfg.SystemPrompt = os.Getenv("KADENCE_SYSTEM_PROMPT")

	cfg.GuardrailEnabled = envBoolOr("KADENCE_GUARDRAIL_ENABLED", false)
	cfg.GuardrailModel = os.Getenv("KADENCE_GUARDRAIL_MODEL")
	cfg.GuardrailBaseURL = os.Getenv("KADENCE_GUARDRAIL_BASE_URL")
	cfg.GuardrailAPIKey = os.Getenv("KADENCE_GUARDRAIL_API_KEY")
	cfg.GuardrailHistoryWindow = envIntOr("KADENCE_GUARDRAIL_HISTORY_WINDOW", 6)
	cfg.DomainName = envOr("KADENCE_DOMAIN_NAME", defaultDomainName)
	cfg.AllowedTopics = envOr("KADENCE_ALLOWED_TOPICS", defaultAllowedTopics)
	cfg.RefusalMessage = envOr("KADENCE_REFUSAL_MESSAGE", defaultRefusalMessage)

	cfg.EmbedBaseURL = envOr("KADENCE_EMBED_BASE_URL", "https://api.openai.com/v1")
	cfg.EmbedAPIKey = os.Getenv("KADENCE_EMBED_API_KEY")
	cfg.EmbedModel = envOr("KADENCE_EMBED_MODEL", "text-embedding-3-small")
	cfg.RAGTopK = envIntOr("KADENCE_RAG_TOP_K", 5)

	cfg.UploadMaxBytes = envIntOr("KADENCE_UPLOAD_MAX_BYTES", 10485760)
	cfg.IngestChunkChars = envIntOr("KADENCE_INGEST_CHUNK_CHARS", 1000)

	cfg.MCPMaxIterations = envIntOr("KADENCE_MCP_MAX_ITERATIONS", 5)
	cfg.MCPMaxTools = envIntOr("KADENCE_MCP_MAX_TOOLS", 100)

	return cfg
}

// IsProd reports whether the server runs in the production environment.
// Accepts both "prod" and "production" so a conventional KADENCE_ENV value
// still enables production behaviour (Secure cookies, strict CSRF origin checks).
func (c Config) IsProd() bool { return c.Env == envProd || c.Env == envProduction }

// ChatEnabled reports whether LLM chat is configured.
func (c Config) ChatEnabled() bool { return c.LLMAPIKey != "" }

// RAGEnabled reports whether retrieval-augmented memory is configured.
func (c Config) RAGEnabled() bool { return c.EmbedAPIKey != "" }

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

// ResolvedGuardrailModel returns the guardrail model, falling back to the chat model.
func (c Config) ResolvedGuardrailModel() string {
	if c.GuardrailModel != "" {
		return c.GuardrailModel
	}
	return c.LLMModel
}

// ResolvedGuardrailBaseURL returns the guardrail base URL, falling back to the chat base URL.
func (c Config) ResolvedGuardrailBaseURL() string {
	if c.GuardrailBaseURL != "" {
		return c.GuardrailBaseURL
	}
	return c.LLMBaseURL
}

// ResolvedGuardrailAPIKey returns the guardrail API key, falling back to the chat API key.
func (c Config) ResolvedGuardrailAPIKey() string {
	if c.GuardrailAPIKey != "" {
		return c.GuardrailAPIKey
	}
	return c.LLMAPIKey
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

func envBoolOr(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func loadTrustedOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var origins []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	if len(origins) == 0 {
		return nil
	}
	return origins
}
