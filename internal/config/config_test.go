package config

import (
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("KADENCE_LISTEN_ADDR", "")
	t.Setenv("KADENCE_ENV", "")

	cfg := Load()

	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
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
	t.Setenv("KADENCE_ENV", "prod")

	cfg := Load()

	if cfg.ListenAddr != ":9090" {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
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
	cfg := Config{DatabaseURL: "postgres://x", Env: "prod"}
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
	t.Setenv("KADENCE_DATABASE_URL", "postgres://x")
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
	t.Setenv("KADENCE_DATABASE_URL", "postgres://x")
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
	t.Setenv("KADENCE_DATABASE_URL", "postgres://x")
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

func TestLoad_WebAuthnRPID(t *testing.T) {
	t.Setenv("KADENCE_WEBAUTHN_RP_ID", "  kadence.example.com  ")
	cfg := Load()
	if cfg.WebAuthnRPID != "kadence.example.com" {
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
