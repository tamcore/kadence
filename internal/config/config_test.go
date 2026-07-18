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
