package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// envelopeVersion prefixes a context-bound blob and is bound as associated
// data, so a future version cannot be reinterpreted as this one.
const envelopeVersion byte = 1

// Record names the kind of secret a blob holds. It is part of the associated
// data, so a refresh token cannot be opened as an access token.
// These are record labels bound as associated data, not credentials.
const (
	RecordAccessToken  = "mcp_oauth_access"  // #nosec G101
	RecordRefreshToken = "mcp_oauth_refresh" // #nosec G101
	RecordPKCEVerifier = "mcp_oauth_pkce"    // #nosec G101
)

// Context is the position a blob was sealed for: which user, which server,
// which kind of secret. Sealing binds it, so a ciphertext moved to another row
// fails to open instead of decrypting into the wrong identity.
type Context struct {
	UserID   int64
	ServerID string
	Record   string
}

func (b Context) validate() error {
	switch {
	case b.UserID <= 0:
		return errors.New("crypto: context needs a user id")
	case strings.TrimSpace(b.ServerID) == "":
		return errors.New("crypto: context needs a server id")
	case strings.TrimSpace(b.Record) == "":
		return errors.New("crypto: context needs a record name")
	}
	return nil
}

func (b Context) aad() []byte {
	return []byte("v" + strconv.Itoa(int(envelopeVersion)) + "|" +
		strconv.FormatInt(b.UserID, 10) + "|" + b.ServerID + "|" + b.Record)
}

// SealWithContext returns version||nonce||ciphertext, binding bind as
// associated data.
func (c *Cipher) SealWithContext(plaintext string, bind Context) ([]byte, error) {
	if err := bind.validate(); err != nil {
		return nil, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, envelopeVersion)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, []byte(plaintext), bind.aad()), nil
}

// OpenWithContext reverses SealWithContext. It fails on a wrong key, a tampered
// blob, a foreign format, or a blob sealed for another context.
func (c *Cipher) OpenWithContext(blob []byte, bind Context) (string, error) {
	if err := bind.validate(); err != nil {
		return "", err
	}
	ns := c.aead.NonceSize()
	if len(blob) < 1+ns {
		return "", errors.New("crypto: sealed blob too short")
	}
	if blob[0] != envelopeVersion {
		return "", fmt.Errorf("crypto: unknown envelope version %d", blob[0])
	}
	nonce, ct := blob[1:1+ns], blob[1+ns:]
	pt, err := c.aead.Open(nil, nonce, ct, bind.aad())
	if err != nil {
		return "", fmt.Errorf("crypto: open: %w", err)
	}
	return string(pt), nil
}
