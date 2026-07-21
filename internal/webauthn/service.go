// Package webauthn wraps github.com/go-webauthn/webauthn with Kadence's
// config, user model, and credential storage.
package webauthn

import (
	"fmt"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/tamcore/kadence/internal/config"
)

const rpDisplayName = "Kadence"

// Service wraps a configured *gwa.WebAuthn relying party.
type Service struct{ wa *gwa.WebAuthn }

// NewService builds a Service from Kadence config. RP origins reuse the
// CSRF TrustedOrigins list; the display name is a fixed constant.
func NewService(cfg config.Config) (*Service, error) {
	wa, err := gwa.New(&gwa.Config{
		RPID:          cfg.WebAuthnRPID,
		RPDisplayName: rpDisplayName,
		RPOrigins:     cfg.TrustedOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: new: %w", err)
	}
	return &Service{wa: wa}, nil
}

// BeginRegistration starts a passkey registration requiring a discoverable
// (resident) credential.
func (s *Service) BeginRegistration(u gwa.User) (*protocol.CredentialCreation, *gwa.SessionData, error) {
	return s.wa.BeginRegistration(u, gwa.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
}

// FinishRegistration verifies an attestation and returns the new credential.
func (s *Service) FinishRegistration(u gwa.User, sess gwa.SessionData, r *http.Request) (*gwa.Credential, error) {
	return s.wa.FinishRegistration(u, sess, r)
}

// BeginDiscoverableLogin starts a usernameless assertion (empty allowCredentials).
func (s *Service) BeginDiscoverableLogin() (*protocol.CredentialAssertion, *gwa.SessionData, error) {
	return s.wa.BeginDiscoverableLogin()
}

// FinishDiscoverableLogin verifies an assertion, resolving the user via handler.
func (s *Service) FinishDiscoverableLogin(h gwa.DiscoverableUserHandler, sess gwa.SessionData, r *http.Request) (*gwa.Credential, error) {
	return s.wa.FinishDiscoverableLogin(h, sess, r)
}
