package auth

import (
	"crypto/rand"
	"encoding/hex"
)

// NewSessionID returns a 64-char hex string from 32 random bytes.
func NewSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
