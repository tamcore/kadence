package webauthn_test

import (
	"bytes"
	"testing"

	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/webauthn"
)

func TestToCredential_RoundTrip(t *testing.T) {
	mc := model.WebAuthnCredential{
		CredentialID: []byte{1, 2, 3}, PublicKey: []byte{9, 8},
		AAGUID: []byte{4, 5}, SignCount: 7, Transports: []string{"internal", "hybrid"},
		BackupEligible: true, BackupState: true,
	}
	c := webauthn.ToCredential(mc)
	if !bytes.Equal(c.ID, mc.CredentialID) || !bytes.Equal(c.PublicKey, mc.PublicKey) {
		t.Fatal("id/pubkey not mapped")
	}
	if c.Authenticator.SignCount != 7 || !bytes.Equal(c.Authenticator.AAGUID, mc.AAGUID) {
		t.Fatal("authenticator not mapped")
	}
	if len(c.Transport) != 2 || string(c.Transport[0]) != "internal" {
		t.Fatalf("transports = %v", c.Transport)
	}
	if !c.Flags.BackupEligible || !c.Flags.BackupState {
		t.Fatalf("flags not mapped: %+v", c.Flags)
	}
}

func TestFromCredential(t *testing.T) {
	cred := &gwa.Credential{ID: []byte{1}, PublicKey: []byte{2}}
	cred.Authenticator.AAGUID = []byte{3}
	cred.Authenticator.SignCount = 5
	cred.Flags = gwa.CredentialFlags{BackupEligible: true, BackupState: true}
	mc := webauthn.FromCredential(cred, 42, "MacBook")
	if mc.UserID != 42 || mc.Name != "MacBook" || mc.SignCount != 5 {
		t.Fatalf("bad mapping: %+v", mc)
	}
	if !mc.BackupEligible || !mc.BackupState {
		t.Fatalf("backup flags not mapped: %+v", mc)
	}
}
