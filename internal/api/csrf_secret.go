package api

import (
	"crypto/rand"
	"log/slog"
)

// randomSecret returns a 32-byte random CSRF secret for dev use when none is set.
func randomSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("generate csrf secret", "err", err)
	}
	return b
}
