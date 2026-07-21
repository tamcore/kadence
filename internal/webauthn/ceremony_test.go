package webauthn_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/webauthn"
)

func newCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return c
}

func TestCeremony_RoundTrip(t *testing.T) {
	cipher := newCipher(t)
	cfg := config.Config{}
	rec := httptest.NewRecorder()
	sess := &gwa.SessionData{Challenge: "abc123", UserVerification: "preferred"}
	if err := webauthn.WriteCeremony(rec, cfg, cipher, sess); err != nil {
		t.Fatalf("write: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	got, err := webauthn.ReadCeremony(req, cipher)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Challenge != "abc123" {
		t.Fatalf("challenge = %q", got.Challenge)
	}
}

func TestReadCeremony_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	if _, err := webauthn.ReadCeremony(req, newCipher(t)); err == nil {
		t.Fatal("expected error for missing cookie")
	}
}

func TestReadCeremony_Tampered(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "wa_ceremony", Value: "not-valid-base64url-or-cipher"})
	if _, err := webauthn.ReadCeremony(req, newCipher(t)); err == nil {
		t.Fatal("expected error for tampered cookie")
	}
}

func TestReadCeremony_WrongKey(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := webauthn.WriteCeremony(rec, config.Config{}, newCipher(t), &gwa.SessionData{Challenge: "x"}); err != nil {
		t.Fatal(err)
	}
	other, _ := crypto.NewCipher([]byte("this-is-a-different-32-byte-key!"))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if _, err := webauthn.ReadCeremony(req, other); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}
