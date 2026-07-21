package webauthn

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/crypto"
)

const (
	ceremonyCookie = "wa_ceremony"
	ceremonyTTL    = 5 * time.Minute
)

// ErrNoCeremony indicates the ceremony cookie is absent.
var ErrNoCeremony = errors.New("webauthn: no ceremony cookie")

// WriteCeremony serializes + encrypts SessionData into the short-lived
// wa_ceremony cookie.
func WriteCeremony(w http.ResponseWriter, cfg config.Config, cipher *crypto.Cipher, sess *gwa.SessionData) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("webauthn: marshal session: %w", err)
	}
	blob, err := cipher.Encrypt(string(raw))
	if err != nil {
		return fmt.Errorf("webauthn: encrypt session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name: ceremonyCookie, Value: base64.RawURLEncoding.EncodeToString(blob), Path: "/",
		MaxAge: int(ceremonyTTL.Seconds()), HttpOnly: true, Secure: cfg.IsProd(), SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// ReadCeremony reads, decodes, decrypts, and unmarshals the ceremony cookie.
func ReadCeremony(r *http.Request, cipher *crypto.Cipher) (gwa.SessionData, error) {
	c, err := r.Cookie(ceremonyCookie)
	if err != nil || c.Value == "" {
		return gwa.SessionData{}, ErrNoCeremony
	}
	blob, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return gwa.SessionData{}, fmt.Errorf("webauthn: decode ceremony: %w", err)
	}
	raw, err := cipher.Decrypt(blob)
	if err != nil {
		return gwa.SessionData{}, fmt.Errorf("webauthn: decrypt ceremony: %w", err)
	}
	var sess gwa.SessionData
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return gwa.SessionData{}, fmt.Errorf("webauthn: unmarshal ceremony: %w", err)
	}
	return sess, nil
}

// ClearCeremony expires the ceremony cookie.
func ClearCeremony(w http.ResponseWriter, cfg config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name: ceremonyCookie, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0),
		HttpOnly: true, Secure: cfg.IsProd(), SameSite: http.SameSiteLaxMode,
	})
}
