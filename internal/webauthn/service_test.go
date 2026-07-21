package webauthn_test

import (
	"testing"

	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/webauthn"
)

func TestNewService_OK(t *testing.T) {
	cfg := config.Config{WebAuthnRPID: "kadence.example.com", TrustedOrigins: []string{"https://kadence.example.com"}}
	svc, err := webauthn.NewService(cfg)
	if err != nil || svc == nil {
		t.Fatalf("NewService err=%v svc=%v", err, svc)
	}
}

func TestNewService_BadConfig(t *testing.T) {
	// No RP ID -> library rejects the config.
	if _, err := webauthn.NewService(config.Config{}); err == nil {
		t.Fatal("expected error for empty RP config")
	}
}

const testUsername = "alice"

func TestUser_ImplementsInterface(t *testing.T) {
	u := webauthn.User{Handle: "h-123", Username: testUsername, Display: ""}
	if got := string(u.WebAuthnID()); got != "h-123" {
		t.Fatalf("WebAuthnID = %q", got)
	}
	if u.WebAuthnName() != testUsername {
		t.Fatalf("WebAuthnName = %q", u.WebAuthnName())
	}
	if u.WebAuthnDisplayName() != testUsername { // falls back to username when Display empty
		t.Fatalf("WebAuthnDisplayName = %q", u.WebAuthnDisplayName())
	}
}
