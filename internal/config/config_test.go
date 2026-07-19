package config

import "testing"

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
	if cfg := Load(); cfg.MCPMaxIterations != 5 {
		t.Fatalf("MCPMaxIterations default = %d, want 5", cfg.MCPMaxIterations)
	}
}
