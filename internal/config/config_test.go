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
