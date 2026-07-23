package config

import (
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
)

// testDatabaseURL is a placeholder DSN used across tests that only need
// Validate() to see a non-empty DatabaseURL.
const testDatabaseURL = "postgres://x"

// testWebAuthnRPID is a placeholder WebAuthn Relying Party ID used across
// WebAuthn-related tests.
const testWebAuthnRPID = "kadence.example.com"

// validConfig returns a Config that passes Validate() outright: every field
// Validate() range-checks is set to a sane positive value (mirroring Load()'s
// defaults), so tests that exercise exactly one Validate() rule can start
// from this baseline and override only the field(s) under test, without
// tripping unrelated range checks.
func validConfig() Config {
	return Config{
		DatabaseURL:            testDatabaseURL,
		LLMContextBudgetTokens: 32000,
		LLMMaxTokens:           2048,
		LLMTimeout:             300,
		RAGTopK:                5,
		MCPMaxIterations:       16,
		MCPMaxTools:            100,
		UploadMaxBytes:         10485760,
		IngestChunkChars:       1000,
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("KADENCE_LISTEN_ADDR", "")
	t.Setenv("KADENCE_HEALTH_ADDR", "")
	t.Setenv("KADENCE_ENV", "")

	cfg := Load()

	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.HealthAddr != ":8081" {
		t.Fatalf("HealthAddr = %q, want %q", cfg.HealthAddr, ":8081")
	}
	if cfg.Env != "dev" {
		t.Fatalf("Env = %q, want %q", cfg.Env, "dev")
	}
	if cfg.IsProd() {
		t.Fatal("IsProd() = true, want false for default env")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("KADENCE_LISTEN_ADDR", ":9090")
	t.Setenv("KADENCE_HEALTH_ADDR", ":9091")
	t.Setenv("KADENCE_ENV", "prod")

	cfg := Load()

	if cfg.ListenAddr != ":9090" {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
	if cfg.HealthAddr != ":9091" {
		t.Fatalf("HealthAddr = %q, want %q", cfg.HealthAddr, ":9091")
	}
	if !cfg.IsProd() {
		t.Fatal("IsProd() = false, want true when KADENCE_ENV=prod")
	}
}

func TestLoadAuthFields(t *testing.T) {
	t.Setenv("KADENCE_DATABASE_URL", "postgres://u:p@localhost:5432/kadence?sslmode=disable")
	t.Setenv("KADENCE_CSRF_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("KADENCE_ADMIN_USERNAME", "admin")
	t.Setenv("KADENCE_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("KADENCE_ADMIN_PASSWORD", "s3cret-pass")

	cfg := Load()

	if cfg.DatabaseURL == "" || cfg.CSRFSecret == "" || cfg.AdminUsername != "admin" {
		t.Fatalf("auth fields not loaded: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRequiresDatabaseURL(t *testing.T) {
	cfg := Config{} // no DatabaseURL
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing DatabaseURL")
	}
}

func TestValidateRequiresCSRFSecretInProd(t *testing.T) {
	cfg := Config{DatabaseURL: testDatabaseURL, Env: "prod"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing CSRF secret in prod")
	}
}

func TestLoadLLMFields(t *testing.T) {
	t.Setenv("KADENCE_LLM_BASE_URL", "https://api.example.com/v1")
	t.Setenv("KADENCE_LLM_API_KEY", "sk-test")
	t.Setenv("KADENCE_LLM_MODEL", "some-model")

	cfg := Load()

	if cfg.LLMBaseURL != "https://api.example.com/v1" || cfg.LLMModel != "some-model" {
		t.Fatalf("llm fields not loaded: %+v", cfg)
	}
	if !cfg.ChatEnabled() {
		t.Fatal("ChatEnabled() = false, want true when LLM API key set")
	}
}

func TestChatDisabledWithoutKey(t *testing.T) {
	t.Setenv("KADENCE_LLM_API_KEY", "")
	if Load().ChatEnabled() {
		t.Fatal("ChatEnabled() = true, want false without API key")
	}
}

func TestLLMDefaults(t *testing.T) {
	t.Setenv("KADENCE_LLM_BASE_URL", "")
	t.Setenv("KADENCE_LLM_MODEL", "")
	cfg := Load()
	if cfg.LLMBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("default base url = %q", cfg.LLMBaseURL)
	}
	if cfg.LLMModel == "" || cfg.LLMMaxTokens == 0 || cfg.LLMTimeout == 0 {
		t.Fatalf("llm defaults not applied: %+v", cfg)
	}
}

func TestLLMContextBudgetDefault(t *testing.T) {
	t.Setenv("KADENCE_LLM_CONTEXT_BUDGET", "")
	if cfg := Load(); cfg.LLMContextBudgetTokens != 32000 {
		t.Fatalf("LLMContextBudgetTokens default = %d, want 32000", cfg.LLMContextBudgetTokens)
	}
}

func TestLLMContextBudgetOverride(t *testing.T) {
	t.Setenv("KADENCE_LLM_CONTEXT_BUDGET", "8000")
	if cfg := Load(); cfg.LLMContextBudgetTokens != 8000 {
		t.Fatalf("LLMContextBudgetTokens = %d, want 8000", cfg.LLMContextBudgetTokens)
	}
}

func TestValidateRejectsNonPositiveContextBudget(t *testing.T) {
	for _, v := range []int{0, -1} {
		cfg := Config{DatabaseURL: testDatabaseURL, LLMContextBudgetTokens: v}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() = nil for LLMContextBudgetTokens=%d, want error", v)
		}
	}
}

func TestValidateAllowsPositiveContextBudget(t *testing.T) {
	cfg := validConfig()
	cfg.LLMContextBudgetTokens = 32000
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for positive LLMContextBudgetTokens", err)
	}
}

func TestGuardrailDefaults(t *testing.T) {
	t.Setenv("KADENCE_GUARDRAIL_ENABLED", "")
	cfg := Load()
	if cfg.GuardrailEnabled {
		t.Fatal("guardrail should default OFF")
	}
	if cfg.GuardrailHistoryWindow != 6 {
		t.Fatalf("history window = %d, want 6", cfg.GuardrailHistoryWindow)
	}
	if cfg.DomainName == "" || cfg.AllowedTopics == "" || cfg.RefusalMessage == "" {
		t.Fatalf("domain defaults missing: %+v", cfg)
	}
}

func TestGuardrailResolversFallBackToLLM(t *testing.T) {
	t.Setenv("KADENCE_LLM_MODEL", "main-model")
	t.Setenv("KADENCE_LLM_BASE_URL", "https://main.example/v1")
	t.Setenv("KADENCE_LLM_API_KEY", "main-key")
	t.Setenv("KADENCE_GUARDRAIL_MODEL", "")
	t.Setenv("KADENCE_GUARDRAIL_BASE_URL", "")
	t.Setenv("KADENCE_GUARDRAIL_API_KEY", "")

	cfg := Load()
	if cfg.ResolvedGuardrailModel() != "main-model" ||
		cfg.ResolvedGuardrailBaseURL() != "https://main.example/v1" ||
		cfg.ResolvedGuardrailAPIKey() != "main-key" {
		t.Fatalf("resolvers should fall back to LLM values: %+v", cfg)
	}
}

func TestGuardrailSeparateBackend(t *testing.T) {
	t.Setenv("KADENCE_LLM_MODEL", "main-model")
	t.Setenv("KADENCE_GUARDRAIL_MODEL", "cheap-model")
	t.Setenv("KADENCE_GUARDRAIL_BASE_URL", "https://guard.example/v1")
	t.Setenv("KADENCE_GUARDRAIL_API_KEY", "guard-key")
	cfg := Load()
	if cfg.ResolvedGuardrailModel() != "cheap-model" || cfg.ResolvedGuardrailBaseURL() != "https://guard.example/v1" || cfg.ResolvedGuardrailAPIKey() != "guard-key" {
		t.Fatalf("resolvers should use guardrail-specific values: %+v", cfg)
	}
}

func TestEmbedDefaultsAndRAGGating(t *testing.T) {
	t.Setenv("KADENCE_EMBED_BASE_URL", "")
	t.Setenv("KADENCE_EMBED_MODEL", "")
	t.Setenv("KADENCE_EMBED_API_KEY", "")
	cfg := Load()
	if cfg.EmbedBaseURL != "https://api.openai.com/v1" || cfg.EmbedModel == "" || cfg.RAGTopK != 5 {
		t.Fatalf("embed defaults wrong: %+v", cfg)
	}
	if cfg.RAGEnabled() {
		t.Fatal("RAG should be disabled without an embed API key")
	}
	t.Setenv("KADENCE_EMBED_API_KEY", "ek")
	if !Load().RAGEnabled() {
		t.Fatal("RAG should enable when embed API key is set")
	}
}

func TestMarkitdownDefaultsAndGating(t *testing.T) {
	t.Setenv("KADENCE_MARKITDOWN_URL", "")
	t.Setenv("KADENCE_MARKITDOWN_AUTH_USER", "")
	t.Setenv("KADENCE_MARKITDOWN_AUTH_PASS", "")
	t.Setenv("KADENCE_MARKITDOWN_TRANSPORT", "")

	cfg := Load()

	if cfg.MarkitdownTransport != "streamable-http" {
		t.Fatalf("MarkitdownTransport = %q, want %q", cfg.MarkitdownTransport, "streamable-http")
	}
	if cfg.MarkitdownEnabled() {
		t.Fatal("MarkitdownEnabled() = true, want false without a markitdown URL")
	}

	t.Setenv("KADENCE_MARKITDOWN_URL", "http://markitdown.internal/mcp")
	t.Setenv("KADENCE_MARKITDOWN_AUTH_USER", "u")
	t.Setenv("KADENCE_MARKITDOWN_AUTH_PASS", "p")
	t.Setenv("KADENCE_MARKITDOWN_TRANSPORT", "sse")

	cfg = Load()

	if !cfg.MarkitdownEnabled() {
		t.Fatal("MarkitdownEnabled() = false, want true when markitdown URL set")
	}
	if cfg.MarkitdownURL != "http://markitdown.internal/mcp" || cfg.MarkitdownAuthUser != "u" ||
		cfg.MarkitdownAuthPass != "p" || cfg.MarkitdownTransport != "sse" {
		t.Fatalf("markitdown fields not loaded: %+v", cfg)
	}
}

func TestIngestDefaults(t *testing.T) {
	t.Setenv("KADENCE_UPLOAD_MAX_BYTES", "")
	t.Setenv("KADENCE_INGEST_CHUNK_CHARS", "")
	cfg := Load()
	if cfg.UploadMaxBytes != 10485760 || cfg.IngestChunkChars != 1000 {
		t.Fatalf("ingest defaults wrong: max=%d chunk=%d", cfg.UploadMaxBytes, cfg.IngestChunkChars)
	}
}

func TestMCPMaxIterationsDefault(t *testing.T) {
	t.Setenv("KADENCE_MCP_MAX_ITERATIONS", "")
	if cfg := Load(); cfg.MCPMaxIterations != 16 {
		t.Fatalf("MCPMaxIterations default = %d, want 16", cfg.MCPMaxIterations)
	}
}

func TestMCPMaxToolsDefault(t *testing.T) {
	t.Setenv("KADENCE_MCP_MAX_TOOLS", "")
	if cfg := Load(); cfg.MCPMaxTools != 100 {
		t.Fatalf("MCPMaxTools default = %d, want 100", cfg.MCPMaxTools)
	}
}

func TestIsProdAcceptsProdAndProduction(t *testing.T) {
	for _, v := range []string{"prod", "production"} {
		if !(Config{Env: v}).IsProd() {
			t.Fatalf("IsProd should be true for Env=%q", v)
		}
	}
	for _, v := range []string{"dev", "", "staging"} {
		if (Config{Env: v}).IsProd() {
			t.Fatalf("IsProd should be false for Env=%q", v)
		}
	}
}

func TestLoadTrustedOrigins(t *testing.T) {
	t.Setenv("KADENCE_TRUSTED_ORIGINS", "a.example.com, b.example.com")

	cfg := Load()

	want := []string{"a.example.com", "b.example.com"}
	if len(cfg.TrustedOrigins) != len(want) {
		t.Fatalf("TrustedOrigins = %v, want %v", cfg.TrustedOrigins, want)
	}
	for i, v := range cfg.TrustedOrigins {
		if v != want[i] {
			t.Fatalf("TrustedOrigins[%d] = %q, want %q", i, v, want[i])
		}
	}
}

func TestLoadTrustedOriginsUnset(t *testing.T) {
	t.Setenv("KADENCE_TRUSTED_ORIGINS", "")

	cfg := Load()

	if len(cfg.TrustedOrigins) > 0 {
		t.Fatalf("TrustedOrigins = %v, want nil/empty when unset", cfg.TrustedOrigins)
	}
}

func TestLoadTrustedOriginsWithWhitespace(t *testing.T) {
	t.Setenv("KADENCE_TRUSTED_ORIGINS", "  a.example.com  ,  b.example.com  ,  ")

	cfg := Load()

	want := []string{"a.example.com", "b.example.com"}
	if len(cfg.TrustedOrigins) != len(want) {
		t.Fatalf("TrustedOrigins = %v, want %v", cfg.TrustedOrigins, want)
	}
	for i, v := range cfg.TrustedOrigins {
		if v != want[i] {
			t.Fatalf("TrustedOrigins[%d] = %q, want %q", i, v, want[i])
		}
	}
}

func TestUserMCPConfig(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("KADENCE_ENCRYPTION_KEY", key)
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", " a.example.io , *.foo.io ,")
	c := Load()
	if len(c.EncryptionKey) != 32 {
		t.Fatalf("EncryptionKey len=%d want 32", len(c.EncryptionKey))
	}
	if got := c.UserMCPAllowedHosts; len(got) != 2 || got[0] != "a.example.io" || got[1] != "*.foo.io" {
		t.Fatalf("UserMCPAllowedHosts=%#v want [a.example.io *.foo.io]", got)
	}
	if !c.UserMCPEnabled() {
		t.Fatal("UserMCPEnabled=false want true")
	}
}

func TestUserMCPDisabledWhenNoKey(t *testing.T) {
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", "a.example.io")
	c := Load()
	if c.UserMCPEnabled() {
		t.Fatal("UserMCPEnabled=true want false (no key)")
	}
}

func TestValidateRequiresEncryptionKeyInProdWhenAllowlistSet(t *testing.T) {
	t.Setenv("KADENCE_DATABASE_URL", testDatabaseURL)
	t.Setenv("KADENCE_ENV", "prod")
	t.Setenv("KADENCE_CSRF_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", "a.example.io")
	t.Setenv("KADENCE_ENCRYPTION_KEY", "")

	cfg := Load()

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for missing encryption key in prod with allowlist set")
	}
	const wantSubstring = "KADENCE_ENCRYPTION_KEY must be a base64-encoded 32-byte key"
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("Validate() error = %q, want substring %q", err.Error(), wantSubstring)
	}
}

func TestValidatePassesInProdWithValidEncryptionKeyAndAllowlist(t *testing.T) {
	t.Setenv("KADENCE_DATABASE_URL", testDatabaseURL)
	t.Setenv("KADENCE_ENV", "prod")
	t.Setenv("KADENCE_CSRF_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", "a.example.io")
	t.Setenv("KADENCE_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	cfg := Load()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil with valid 32-byte encryption key", err)
	}
}

func TestValidateSkipsEncryptionKeyRuleOutsideProd(t *testing.T) {
	t.Setenv("KADENCE_DATABASE_URL", testDatabaseURL)
	t.Setenv("KADENCE_ENV", "")
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", "a.example.io")
	t.Setenv("KADENCE_ENCRYPTION_KEY", "")

	cfg := Load()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil outside prod even without encryption key", err)
	}
}

func TestDecodeKeyInvalidBase64YieldsNilKeyAndDisablesUserMCP(t *testing.T) {
	t.Setenv("KADENCE_ENCRYPTION_KEY", "not-valid-base64!!!")
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", "a.example.io")

	cfg := Load()

	if len(cfg.EncryptionKey) != 0 {
		t.Fatalf("EncryptionKey len=%d, want 0 for invalid base64", len(cfg.EncryptionKey))
	}
	if cfg.UserMCPEnabled() {
		t.Fatal("UserMCPEnabled() = true, want false for invalid base64 key")
	}
}

func TestUserMCPDisabledWithWrongKeyLength(t *testing.T) {
	t.Setenv("KADENCE_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	t.Setenv("KADENCE_USER_MCP_ALLOWED_HOSTS", "a.example.io")

	cfg := Load()

	if cfg.UserMCPEnabled() {
		t.Fatal("UserMCPEnabled() = true, want false for 16-byte key")
	}
}

// TestValidateFailsFastOnTypoedEncryptionKey covers WAVE2-hardening.md §12:
// a KADENCE_ENCRYPTION_KEY that was set but is invalid (bad base64, or valid
// base64 of the wrong length) must fail Validate() outright — a typo must
// not silently disable passkeys/user-MCP.
func TestValidateFailsFastOnTypoedEncryptionKey(t *testing.T) {
	cases := map[string]string{
		"invalid base64":       "not-valid-base64!!!",
		"wrong decoded length": base64.StdEncoding.EncodeToString(make([]byte, 16)),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("KADENCE_DATABASE_URL", testDatabaseURL)
			t.Setenv("KADENCE_ENCRYPTION_KEY", raw)

			cfg := Load()

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want error for a typo'd KADENCE_ENCRYPTION_KEY")
			}
			if !strings.Contains(err.Error(), "KADENCE_ENCRYPTION_KEY") {
				t.Fatalf("Validate() error = %q, want it to name KADENCE_ENCRYPTION_KEY", err.Error())
			}
		})
	}
}

// TestValidatePassesWithNoEncryptionKeySet ensures leaving
// KADENCE_ENCRYPTION_KEY entirely unset (the common case: passkeys/user-MCP
// simply not used) is not itself a Validate() failure.
func TestValidatePassesWithNoEncryptionKeySet(t *testing.T) {
	t.Setenv("KADENCE_DATABASE_URL", testDatabaseURL)
	t.Setenv("KADENCE_ENCRYPTION_KEY", "")

	cfg := Load()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil when KADENCE_ENCRYPTION_KEY is simply unset", err)
	}
}

// TestValidateRangeChecks is the table-driven range-check test for
// WAVE2-hardening.md §12's field list: each field must be a positive integer
// (or, for MaxBodyBytes, non-negative — 0 falls back to a default via
// ResolvedMaxBodyBytes).
func TestValidateRangeChecks(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantEnv string
	}{
		{"LLMMaxTokens", func(c *Config) { c.LLMMaxTokens = 0 }, "KADENCE_LLM_MAX_TOKENS"},
		{"LLMTimeout", func(c *Config) { c.LLMTimeout = 0 }, "KADENCE_LLM_TIMEOUT"},
		{"RAGTopK", func(c *Config) { c.RAGTopK = 0 }, "KADENCE_RAG_TOP_K"},
		{"MCPMaxIterations", func(c *Config) { c.MCPMaxIterations = 0 }, "KADENCE_MCP_MAX_ITERATIONS"},
		{"MCPMaxTools", func(c *Config) { c.MCPMaxTools = 0 }, "KADENCE_MCP_MAX_TOOLS"},
		{"UploadMaxBytes", func(c *Config) { c.UploadMaxBytes = 0 }, "KADENCE_UPLOAD_MAX_BYTES"},
		{"IngestChunkChars", func(c *Config) { c.IngestChunkChars = 0 }, "KADENCE_INGEST_CHUNK_CHARS"},
		{"MaxBodyBytes negative", func(c *Config) { c.MaxBodyBytes = -1 }, "KADENCE_MAX_BODY_BYTES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error for invalid %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantEnv) {
				t.Fatalf("Validate() error = %q, want it to mention %s", err.Error(), tc.wantEnv)
			}
		})
	}
}

// TestValidateAllowsZeroMaxBodyBytes documents that MaxBodyBytes=0 (the
// zero-value default for a Config{} literal not built via Load()) is not a
// Validate() failure: ResolvedMaxBodyBytes() falls back to a sane default.
func TestValidateAllowsZeroMaxBodyBytes(t *testing.T) {
	cfg := validConfig()
	cfg.MaxBodyBytes = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for MaxBodyBytes=0", err)
	}
	if got := cfg.ResolvedMaxBodyBytes(); got != defaultMaxBodyBytes {
		t.Fatalf("ResolvedMaxBodyBytes() = %d, want default %d", got, defaultMaxBodyBytes)
	}
}

// TestValidateRequiresCSRFSecretLengthInProd covers the CSRFSecret >= 32
// bytes requirement in production.
func TestValidateRequiresCSRFSecretLengthInProd(t *testing.T) {
	cfg := validConfig()
	cfg.Env = envProd
	cfg.CSRFSecret = "too-short"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for a CSRF secret shorter than 32 bytes in prod")
	}
	if !strings.Contains(err.Error(), "KADENCE_CSRF_SECRET") {
		t.Fatalf("Validate() error = %q, want it to mention KADENCE_CSRF_SECRET", err.Error())
	}
}

func TestValidateAllowsCSRFSecretOfExactly32BytesInProd(t *testing.T) {
	cfg := validConfig()
	cfg.Env = envProd
	cfg.CSRFSecret = strings.Repeat("a", 32)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a 32-byte CSRF secret in prod", err)
	}
}

// TestValidateWebAuthnPreconditions covers WAVE2-hardening.md §12: Validate()
// must enforce the same preconditions cmd/server/serve.Run relies on before
// constructing webauthn.Service/crypto.Cipher (TrustedOrigins set, and a
// valid 32-byte EncryptionKey), so a misconfigured deployment fails at
// Validate() instead of partway through startup.
func TestValidateWebAuthnPreconditions(t *testing.T) {
	validKey := make([]byte, encryptionKeyLen)

	t.Run("missing trusted origins", func(t *testing.T) {
		cfg := validConfig()
		cfg.WebAuthnRPID = testWebAuthnRPID
		cfg.EncryptionKey = validKey
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "KADENCE_TRUSTED_ORIGINS") {
			t.Fatalf("Validate() = %v, want error mentioning KADENCE_TRUSTED_ORIGINS", err)
		}
	})

	t.Run("missing encryption key", func(t *testing.T) {
		cfg := validConfig()
		cfg.WebAuthnRPID = testWebAuthnRPID
		cfg.TrustedOrigins = []string{"https://kadence.example.com"}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "KADENCE_ENCRYPTION_KEY") {
			t.Fatalf("Validate() = %v, want error mentioning KADENCE_ENCRYPTION_KEY", err)
		}
	})

	t.Run("all preconditions met", func(t *testing.T) {
		cfg := validConfig()
		cfg.WebAuthnRPID = testWebAuthnRPID
		cfg.TrustedOrigins = []string{"https://kadence.example.com"}
		cfg.EncryptionKey = validKey
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil when all WebAuthn preconditions are met", err)
		}
	})

	t.Run("disabled: no RPID means no preconditions enforced", func(t *testing.T) {
		cfg := validConfig()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil when WebAuthn is disabled (no RPID)", err)
		}
	})
}

func TestLoad_WebAuthnRPID(t *testing.T) {
	t.Setenv("KADENCE_WEBAUTHN_RP_ID", "  kadence.example.com  ")
	cfg := Load()
	if cfg.WebAuthnRPID != testWebAuthnRPID {
		t.Fatalf("WebAuthnRPID = %q, want trimmed kadence.example.com", cfg.WebAuthnRPID)
	}
	if !cfg.WebAuthnEnabled() {
		t.Fatal("WebAuthnEnabled() = false, want true when RP ID set")
	}
}

func TestWebAuthnEnabled_EmptyIsDisabled(t *testing.T) {
	if (Config{}).WebAuthnEnabled() {
		t.Fatal("empty RP ID must be disabled")
	}
}

func TestRateLimitDefaults(t *testing.T) {
	t.Setenv("KADENCE_RATE_LIMIT_GLOBAL", "")
	t.Setenv("KADENCE_RATE_LIMIT_AUTH", "")

	cfg := Load()

	if cfg.RateLimitGlobal != 300 {
		t.Fatalf("RateLimitGlobal = %d, want 300", cfg.RateLimitGlobal)
	}
	if cfg.RateLimitAuth != 10 {
		t.Fatalf("RateLimitAuth = %d, want 10", cfg.RateLimitAuth)
	}
}

func TestRateLimitOverrides(t *testing.T) {
	t.Setenv("KADENCE_RATE_LIMIT_GLOBAL", "600")
	t.Setenv("KADENCE_RATE_LIMIT_AUTH", "20")

	cfg := Load()

	if cfg.RateLimitGlobal != 600 {
		t.Fatalf("RateLimitGlobal = %d, want 600", cfg.RateLimitGlobal)
	}
	if cfg.RateLimitAuth != 20 {
		t.Fatalf("RateLimitAuth = %d, want 20", cfg.RateLimitAuth)
	}
}

func TestValidateRejectsNegativeRateLimits(t *testing.T) {
	base := validConfig()

	global := base
	global.RateLimitGlobal = -1
	if err := global.Validate(); err == nil || !strings.Contains(err.Error(), "KADENCE_RATE_LIMIT_GLOBAL") {
		t.Fatalf("Validate() = %v, want error mentioning KADENCE_RATE_LIMIT_GLOBAL", err)
	}

	authCfg := base
	authCfg.RateLimitAuth = -1
	if err := authCfg.Validate(); err == nil || !strings.Contains(err.Error(), "KADENCE_RATE_LIMIT_AUTH") {
		t.Fatalf("Validate() = %v, want error mentioning KADENCE_RATE_LIMIT_AUTH", err)
	}
}

func TestValidateAllowsZeroRateLimits(t *testing.T) {
	cfg := validConfig()
	cfg.RateLimitGlobal = 0
	cfg.RateLimitAuth = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (0 disables rate limiting)", err)
	}
}

func TestEmbedDimensionsDefault(t *testing.T) {
	t.Setenv("KADENCE_EMBED_DIMENSIONS", "")

	cfg := Load()

	if cfg.EmbedDimensions != 1024 {
		t.Fatalf("EmbedDimensions = %d, want 1024", cfg.EmbedDimensions)
	}
}

func TestEmbedDimensionsOverride(t *testing.T) {
	t.Setenv("KADENCE_EMBED_DIMENSIONS", "768")

	cfg := Load()

	if cfg.EmbedDimensions != 768 {
		t.Fatalf("EmbedDimensions = %d, want 768", cfg.EmbedDimensions)
	}
}

func TestValidateRejectsNegativeEmbedDimensions(t *testing.T) {
	cfg := validConfig()
	cfg.EmbedDimensions = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "KADENCE_EMBED_DIMENSIONS") {
		t.Fatalf("Validate() = %v, want error mentioning KADENCE_EMBED_DIMENSIONS", err)
	}
}

func TestValidateAllowsZeroEmbedDimensions(t *testing.T) {
	cfg := validConfig()
	cfg.EmbedDimensions = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (0 disables dimension pinning)", err)
	}
}

func TestUserMCPMaxServersDefault(t *testing.T) {
	t.Setenv("KADENCE_USER_MCP_MAX_SERVERS", "")

	cfg := Load()

	if cfg.UserMCPMaxServers != 10 {
		t.Fatalf("UserMCPMaxServers = %d, want 10", cfg.UserMCPMaxServers)
	}
}

func TestUserMCPMaxServersOverride(t *testing.T) {
	t.Setenv("KADENCE_USER_MCP_MAX_SERVERS", "3")

	cfg := Load()

	if cfg.UserMCPMaxServers != 3 {
		t.Fatalf("UserMCPMaxServers = %d, want 3", cfg.UserMCPMaxServers)
	}
}

func TestValidateRejectsNegativeUserMCPMaxServers(t *testing.T) {
	cfg := validConfig()
	cfg.UserMCPMaxServers = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "KADENCE_USER_MCP_MAX_SERVERS") {
		t.Fatalf("Validate() = %v, want error mentioning KADENCE_USER_MCP_MAX_SERVERS", err)
	}
}

func TestSlogLevelMapping(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"":      slog.LevelInfo,
		"bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := (Config{LogLevel: in}).SlogLevel(); got != want {
			t.Fatalf("SlogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
