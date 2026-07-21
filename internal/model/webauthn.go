package model

import "time"

// WebAuthnCredential is a registered passkey.
type WebAuthnCredential struct {
	ID           int64
	PublicID     string
	UserID       int64
	CredentialID []byte
	PublicKey    []byte
	AAGUID       []byte
	SignCount    uint32
	Transports   []string
	Name         string
	CreatedAt    time.Time
	LastUsedAt   *time.Time // nil until first assertion
}
