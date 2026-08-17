package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewPKCEProducesAVerifierAndItsChallenge(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if len(p.Verifier) != verifierLen {
		t.Fatalf("verifier length = %d, want %d", len(p.Verifier), verifierLen)
	}
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	if strings.ContainsFunc(p.Verifier, func(r rune) bool { return !strings.ContainsRune(unreserved, r) }) {
		t.Fatalf("verifier has a reserved character: %q", p.Verifier)
	}

	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Fatalf("challenge = %q, want %q", p.Challenge, want)
	}
}

func TestNewPKCEIsNotDeterministic(t *testing.T) {
	a, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	b, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if a.Verifier == b.Verifier {
		t.Fatal("two verifiers are identical")
	}
}

func TestNewStateIsUnguessableAndURLSafe(t *testing.T) {
	a, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	b, err := NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if a == b {
		t.Fatal("two states are identical")
	}
	if _, err := base64.RawURLEncoding.DecodeString(a); err != nil {
		t.Fatalf("state is not base64url: %v", err)
	}
	if len(a) < 40 {
		t.Fatalf("state is only %d characters; want at least 40 (32 random bytes)", len(a))
	}
}
