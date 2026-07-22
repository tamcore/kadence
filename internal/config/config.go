// Package config loads server configuration from the environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
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
	// WebAuthnRPID is the WebAuthn Relying Party ID (the site's effective
	// domain, e.g. kadence.example.com), from KADENCE_WEBAUTHN_RP_ID.
	// Empty disables passkeys entirely. Enabling passkeys also requires
	// KADENCE_TRUSTED_ORIGINS (relying-party origins) and a 32-byte
	// KADENCE_ENCRYPTION_KEY (ceremony/session-data cipher); the server
	// fails fast at boot if either is missing.
	WebAuthnRPID string

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
	// LLMContextBudgetTokens bounds how many (estimated) tokens of prior
	// conversation history are sent with each chat request
	// (KADENCE_LLM_CONTEXT_BUDGET), separate from LLMMaxTokens (the
	// completion cap). When history would exceed the budget, whole
	// oldest-middle turns are dropped (never splitting a tool-call/result
	// pair), always keeping the first user message and the newest turns
	// that fit.
	LLMContextBudgetTokens int

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
	// EmbedDimensions pins the embedding vector length (KADENCE_EMBED_DIMENSIONS,
	// default 1024) so it can be stored in a fixed-width pgvector column with an
	// HNSW index. 0 only stops the client from sending the dimensions field and
	// disables client-side truncation; migration 00011 pins the DB column to
	// vector(1024), so 0 must not be used unless the provider natively returns
	// 1024-dim vectors, otherwise inserts/searches fail with a Postgres
	// "different vector dimensions" error.
	EmbedDimensions int

	// Ingestion.
	UploadMaxBytes   int
	IngestChunkChars int

	// MaxBodyBytes caps the request body size for /api routes in general
	// (KADENCE_MAX_BODY_BYTES, default 1 MiB). /api/documents overrides this
	// with the larger UploadMaxBytes at the route level (documents.go wraps
	// r.Body again with its own http.MaxBytesReader), so this global cap
	// never blocks legitimate uploads.
	MaxBodyBytes int

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
	// encryptionKeyErr carries a non-nil error from Load() when
	// KADENCE_ENCRYPTION_KEY was set but failed to decode (bad base64, or
	// the wrong byte length), so Validate() can fail fast on a typo instead
	// of silently treating it as unset. Left nil (no problem) by any Config
	// literal built directly rather than via Load(), e.g. in tests.
	encryptionKeyErr error

	// Rate limiting (per-IP, sliding window). 0 disables the respective limiter.
	// RateLimitGlobal caps all /api requests; RateLimitAuth caps the
	// auth-sensitive endpoints (login, passkey login, credential submission).
	RateLimitGlobal int
	RateLimitAuth   int
}

const (
	defaultListenAddr = ":8080"
	defaultEnv        = "dev"
	envProd           = "prod"
	envProduction     = "production"

	// defaultMaxBodyBytes is both the KADENCE_MAX_BODY_BYTES default and the
	// fallback ResolvedMaxBodyBytes() returns for a zero-value Config (e.g.
	// tests constructing Config{} directly instead of via Load()).
	defaultMaxBodyBytes = 1 << 20 // 1 MiB

	// minProdCSRFSecretLen is the minimum KADENCE_CSRF_SECRET length required
	// in production; gorilla/csrf accepts shorter secrets but a weak secret
	// undermines the CSRF token's unforgeability guarantee.
	minProdCSRFSecretLen = 32

	// encryptionKeyLen is the required decoded length of KADENCE_ENCRYPTION_KEY.
	encryptionKeyLen = 32

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
	cfg.WebAuthnRPID = strings.TrimSpace(os.Getenv("KADENCE_WEBAUTHN_RP_ID"))

	cfg.LLMBaseURL = envOr("KADENCE_LLM_BASE_URL", "https://api.openai.com/v1")
	cfg.LLMAPIKey = os.Getenv("KADENCE_LLM_API_KEY")
	cfg.LLMModel = envOr("KADENCE_LLM_MODEL", "gpt-4o-mini")
	cfg.LLMMaxTokens = envIntOr("KADENCE_LLM_MAX_TOKENS", 2048)
	cfg.LLMTemperature = envFloatOr("KADENCE_LLM_TEMPERATURE", 0.3)
	cfg.LLMTimeout = envDurationOr("KADENCE_LLM_TIMEOUT", 300*time.Second)
	cfg.SystemPrompt = os.Getenv("KADENCE_SYSTEM_PROMPT")
	cfg.LLMContextBudgetTokens = envIntOr("KADENCE_LLM_CONTEXT_BUDGET", 32000)

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
	cfg.EmbedDimensions = envIntOr("KADENCE_EMBED_DIMENSIONS", 1024)

	cfg.UploadMaxBytes = envIntOr("KADENCE_UPLOAD_MAX_BYTES", 10485760)
	cfg.IngestChunkChars = envIntOr("KADENCE_INGEST_CHUNK_CHARS", 1000)
	cfg.MaxBodyBytes = envIntOr("KADENCE_MAX_BODY_BYTES", defaultMaxBodyBytes)

	cfg.MarkitdownURL = os.Getenv("KADENCE_MARKITDOWN_URL")
	cfg.MarkitdownAuthUser = os.Getenv("KADENCE_MARKITDOWN_AUTH_USER")
	cfg.MarkitdownAuthPass = os.Getenv("KADENCE_MARKITDOWN_AUTH_PASS")
	cfg.MarkitdownTransport = envOr("KADENCE_MARKITDOWN_TRANSPORT", "streamable-http")

	cfg.MCPMaxIterations = envIntOr("KADENCE_MCP_MAX_ITERATIONS", 16)
	cfg.MCPMaxTools = envIntOr("KADENCE_MCP_MAX_TOOLS", 100)
	cfg.MCPCAFile = os.Getenv("KADENCE_MCP_CA_FILE")

	key, keyErr := decodeKey(os.Getenv("KADENCE_ENCRYPTION_KEY"))
	cfg.EncryptionKey = key
	cfg.encryptionKeyErr = keyErr
	cfg.UserMCPAllowedHosts = splitCSV(os.Getenv("KADENCE_USER_MCP_ALLOWED_HOSTS"))

	cfg.RateLimitGlobal = envIntOr("KADENCE_RATE_LIMIT_GLOBAL", 300)
	cfg.RateLimitAuth = envIntOr("KADENCE_RATE_LIMIT_AUTH", 10)

	return cfg
}

// IsProd reports whether the server runs in the production environment.
// Accepts both "prod" and "production" so a conventional KADENCE_ENV value
// still enables production behaviour (Secure cookies, strict CSRF origin checks).
func (c Config) IsProd() bool { return c.Env == envProd || c.Env == envProduction }

// WebAuthnEnabled reports whether passkey (WebAuthn) support is configured.
func (c Config) WebAuthnEnabled() bool { return c.WebAuthnRPID != "" }

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
	return len(c.EncryptionKey) == encryptionKeyLen && len(c.UserMCPAllowedHosts) > 0
}

// ResolvedMaxBodyBytes returns the configured global request-body cap,
// falling back to defaultMaxBodyBytes when MaxBodyBytes is unset (e.g. a
// Config{} literal built directly in tests rather than via Load()).
func (c Config) ResolvedMaxBodyBytes() int64 {
	if c.MaxBodyBytes > 0 {
		return int64(c.MaxBodyBytes)
	}
	return defaultMaxBodyBytes
}

// Validate checks required runtime configuration. Call before starting the
// server. This is the single source of truth for "is this configuration
// startable" — cmd/server/serve.Run must not duplicate these checks; it may
// only add further construction-time errors that Validate cannot anticipate
// (e.g. a malformed WebAuthnRPID rejected by the go-webauthn library itself).
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("KADENCE_DATABASE_URL is required")
	}
	if c.IsProd() {
		if c.CSRFSecret == "" {
			return errors.New("KADENCE_CSRF_SECRET is required in production")
		}
		if len(c.CSRFSecret) < minProdCSRFSecretLen {
			return fmt.Errorf("KADENCE_CSRF_SECRET must be at least %d bytes in production", minProdCSRFSecretLen)
		}
	}
	// A KADENCE_ENCRYPTION_KEY that was set but failed to decode (bad base64,
	// or the wrong byte length) is a typo, not "feature disabled": fail fast
	// rather than silently disabling passkeys/user-MCP.
	if c.encryptionKeyErr != nil {
		return fmt.Errorf("KADENCE_ENCRYPTION_KEY is invalid: %w", c.encryptionKeyErr)
	}
	if c.IsProd() && len(c.UserMCPAllowedHosts) > 0 && len(c.EncryptionKey) != encryptionKeyLen {
		return errors.New("KADENCE_ENCRYPTION_KEY must be a base64-encoded 32-byte key when KADENCE_USER_MCP_ALLOWED_HOSTS is set")
	}
	// WebAuthn (passkeys): mirrors the preconditions cmd/server/serve.Run
	// enforces before constructing webauthn.Service/crypto.Cipher, so a
	// misconfigured deployment fails at Validate() rather than partway
	// through startup.
	if c.WebAuthnEnabled() {
		if len(c.TrustedOrigins) == 0 {
			return errors.New("KADENCE_TRUSTED_ORIGINS is required when KADENCE_WEBAUTHN_RP_ID is set")
		}
		if len(c.EncryptionKey) != encryptionKeyLen {
			return errors.New("KADENCE_ENCRYPTION_KEY must be a base64-encoded 32-byte key when KADENCE_WEBAUTHN_RP_ID is set")
		}
	}
	if c.RateLimitGlobal < 0 {
		return errors.New("KADENCE_RATE_LIMIT_GLOBAL must be a non-negative integer")
	}
	if c.RateLimitAuth < 0 {
		return errors.New("KADENCE_RATE_LIMIT_AUTH must be a non-negative integer")
	}
	if c.EmbedDimensions < 0 {
		return errors.New("KADENCE_EMBED_DIMENSIONS must be a non-negative integer")
	}
	if c.LLMContextBudgetTokens <= 0 {
		return errors.New("KADENCE_LLM_CONTEXT_BUDGET must be a positive integer")
	}
	if c.LLMMaxTokens <= 0 {
		return errors.New("KADENCE_LLM_MAX_TOKENS must be a positive integer")
	}
	if c.LLMTimeout <= 0 {
		return errors.New("KADENCE_LLM_TIMEOUT must be a positive duration")
	}
	if c.RAGTopK <= 0 {
		return errors.New("KADENCE_RAG_TOP_K must be a positive integer")
	}
	if c.MCPMaxIterations <= 0 {
		return errors.New("KADENCE_MCP_MAX_ITERATIONS must be a positive integer")
	}
	if c.MCPMaxTools <= 0 {
		return errors.New("KADENCE_MCP_MAX_TOOLS must be a positive integer")
	}
	if c.UploadMaxBytes <= 0 {
		return errors.New("KADENCE_UPLOAD_MAX_BYTES must be a positive integer")
	}
	if c.IngestChunkChars <= 0 {
		return errors.New("KADENCE_INGEST_CHUNK_CHARS must be a positive integer")
	}
	if c.MaxBodyBytes < 0 {
		return errors.New("KADENCE_MAX_BODY_BYTES must be a non-negative integer")
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

// decodeKey base64-decodes an encryption key. Empty input is not an error
// (the key is simply unset); non-empty input that fails to decode, or that
// decodes to anything other than encryptionKeyLen bytes, returns an error so
// the caller (Load, via Config.encryptionKeyErr) can have Validate() fail
// fast on a typo rather than silently disabling the key.
func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(b) != encryptionKeyLen {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", encryptionKeyLen, len(b))
	}
	return b, nil
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
