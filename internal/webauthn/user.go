package webauthn

import gwa "github.com/go-webauthn/webauthn/webauthn"

// User adapts a Kadence user to the gwa.User interface. Handle is the opaque
// per-user webauthn_user_handle (a UUID string); its bytes are the WebAuthn
// user id.
type User struct {
	Handle   string
	Username string
	Display  string
	Creds    []gwa.Credential
}

func (u User) WebAuthnID() []byte { return []byte(u.Handle) }

func (u User) WebAuthnName() string { return u.Username }

func (u User) WebAuthnDisplayName() string {
	if u.Display != "" {
		return u.Display
	}
	return u.Username
}

func (u User) WebAuthnCredentials() []gwa.Credential { return u.Creds }
