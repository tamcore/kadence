// Package config loads server configuration from the environment.
package config

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the Kadence server.
type Config struct {
	ListenAddr string
	Env        string
	// LogLevel is the slog level (KADENCE_LOG_LEVEL): debug|info|warn|error.
	LogLevel string

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

	// Markitdown ingestion extractor (markitdown-mcp). Enabled iff
	// MarkitdownURL != "".
	MarkitdownURL       string
	MarkitdownAuthUser  string
	MarkitdownAuthPass  string
	MarkitdownTransport string

	// MCP orchestration.
	MCPMaxIterations int
	// MCPMaxTools caps the number of MCP tool definitions injected into a
	// single chat request, as defense-in-depth against provider tool-count
	// limits (e.g. OpenAI's 128-tool cap) when many MCP servers are configured.
	MCPMaxTools int
	// MCPCAFile is the path to a PEM-encoded CA certificate used to verify
	// MCP server (and markitdown) TLS certs over HTTPS (KADENCE_MCP_CA_FILE).
	// Empty means no custom CA: MCP traffic uses mcp-go's default HTTP
	// client (plaintext http, or https verified against the system trust
	// store).
	MCPCAFile string

	// User-defined MCP servers. EncryptionKey is a 32-byte key (KADENCE_ENCRYPTION_KEY,
	// base64-encoded) used to encrypt stored per-user MCP server credentials.
	// UserMCPAllowedHosts is the host allowlist (KADENCE_USER_MCP_ALLOWED_HOSTS,
	// comma-split, trimmed) that user-defined MCP server URLs must match.
	EncryptionKey       []byte
	UserMCPAllowedHosts []string
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
		LogLevel:       envOr("KADENCE_LOG_LEVEL", "info"),
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
	cfg.LLMTimeout = envDurationOr("KADENCE_LLM_TIMEOUT", 300*time.Second)
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

	cfg.MarkitdownURL = os.Getenv("KADENCE_MARKITDOWN_URL")
	cfg.MarkitdownAuthUser = os.Getenv("KADENCE_MARKITDOWN_AUTH_USER")
	cfg.MarkitdownAuthPass = os.Getenv("KADENCE_MARKITDOWN_AUTH_PASS")
	cfg.MarkitdownTransport = envOr("KADENCE_MARKITDOWN_TRANSPORT", "streamable-http")

	cfg.MCPMaxIterations = envIntOr("KADENCE_MCP_MAX_ITERATIONS", 16)
	cfg.MCPMaxTools = envIntOr("KADENCE_MCP_MAX_TOOLS", 100)
	cfg.MCPCAFile = os.Getenv("KADENCE_MCP_CA_FILE")

	cfg.EncryptionKey = decodeKey(os.Getenv("KADENCE_ENCRYPTION_KEY"))
	cfg.UserMCPAllowedHosts = splitCSV(os.Getenv("KADENCE_USER_MCP_ALLOWED_HOSTS"))

	return cfg
}

// IsProd reports whether the server runs in the production environment.
// Accepts both "prod" and "production" so a conventional KADENCE_ENV value
// still enables production behaviour (Secure cookies, strict CSRF origin checks).
func (c Config) IsProd() bool { return c.Env == envProd || c.Env == envProduction }

// SlogLevel maps LogLevel to a slog.Level, defaulting to Info for unknown values.
func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ChatEnabled reports whether LLM chat is configured.
func (c Config) ChatEnabled() bool { return c.LLMAPIKey != "" }

// RAGEnabled reports whether retrieval-augmented memory is configured.
func (c Config) RAGEnabled() bool { return c.EmbedAPIKey != "" }

// MarkitdownEnabled reports whether the markitdown-mcp ingestion extractor is configured.
func (c Config) MarkitdownEnabled() bool { return c.MarkitdownURL != "" }

// UserMCPEnabled reports whether user-defined MCP servers can be registered:
// a valid 32-byte encryption key AND at least one allowlisted host.
func (c Config) UserMCPEnabled() bool {
	return len(c.EncryptionKey) == 32 && len(c.UserMCPAllowedHosts) > 0
}

// Validate checks required runtime configuration. Call before starting the server.
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("KADENCE_DATABASE_URL is required")
	}
	if c.IsProd() && c.CSRFSecret == "" {
		return errors.New("KADENCE_CSRF_SECRET is required in production")
	}
	if c.IsProd() && len(c.UserMCPAllowedHosts) > 0 && len(c.EncryptionKey) != 32 {
		return errors.New("KADENCE_ENCRYPTION_KEY must be a base64-encoded 32-byte key when KADENCE_USER_MCP_ALLOWED_HOSTS is set")
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

// decodeKey base64-decodes an encryption key; returns nil on empty/invalid input.
func decodeKey(s string) []byte {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// splitCSV splits a comma-separated env value, trimming spaces and dropping empties.
func splitCSV(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
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
