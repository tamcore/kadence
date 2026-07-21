package webauthn

import (
	"github.com/go-webauthn/webauthn/protocol"
	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/tamcore/kadence/internal/model"
)

// ToCredential reconstructs a gwa.Credential from stored columns. Only the
// fields go-webauthn checks during assertion (ID, PublicKey, AAGUID,
// SignCount, Transport) are needed for discoverable login.
func ToCredential(c model.WebAuthnCredential) gwa.Credential {
	ts := make([]protocol.AuthenticatorTransport, len(c.Transports))
	for i, t := range c.Transports {
		ts[i] = protocol.AuthenticatorTransport(t)
	}
	cred := gwa.Credential{ID: c.CredentialID, PublicKey: c.PublicKey, Transport: ts}
	cred.Authenticator.AAGUID = c.AAGUID
	cred.Authenticator.SignCount = c.SignCount
	return cred
}

// FromCredential maps a freshly registered gwa.Credential to storage.
func FromCredential(cred *gwa.Credential, userID int64, name string) model.WebAuthnCredential {
	ts := make([]string, len(cred.Transport))
	for i, t := range cred.Transport {
		ts[i] = string(t)
	}
	return model.WebAuthnCredential{
		UserID:       userID,
		CredentialID: cred.ID,
		PublicKey:    cred.PublicKey,
		AAGUID:       cred.Authenticator.AAGUID,
		SignCount:    cred.Authenticator.SignCount,
		Transports:   ts,
		Name:         name,
	}
}
