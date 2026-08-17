package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/tamcore/kadence/internal/store"
)

var (
	// ErrNotLinked means this user has not authorized that server.
	ErrNotLinked = errors.New("oauth: not linked")
	// ErrReauthRequired means the stored grant is gone and only a fresh browser
	// authorization can restore it. It is terminal by design: the authorization
	// server revokes a whole token family on any replay, so retrying would
	// destroy a grant that might still be alive.
	ErrReauthRequired = errors.New("oauth: reauthorization required")
	// ErrBadTransaction covers every callback that does not match a live
	// authorization: unknown state, wrong user, wrong browser, already used,
	// expired. They are one error so a caller learns nothing from which.
	ErrBadTransaction = errors.New("oauth: authorization transaction does not match")
	// ErrTooManyAttempts means this user has too many authorizations open.
	ErrTooManyAttempts = errors.New("oauth: too many authorization attempts in flight")
	// ErrUnknownServer means no oauth client is configured for that server.
	ErrUnknownServer = errors.New("oauth: unknown server")
)

const (
	// refreshSkew renews a token this far before it expires, so a call that
	// takes a moment does not arrive with an expired credential.
	refreshSkew = 60 * time.Second
	// transactionTTL matches the authorization server's own 10-minute window.
	transactionTTL = 10 * time.Minute
	// maxOpenTransactions bounds how many authorizations one user may have in
	// flight. An abandoned attempt is a row nobody else collects.
	maxOpenTransactions = 5
)

// LinkStore is the persistence the service needs.
type LinkStore interface {
	Upsert(ctx context.Context, link store.MCPOAuthLink) error
	Get(ctx context.Context, userID int64, serverID string) (store.MCPOAuthLink, error)
	RotateUnderLock(ctx context.Context, userID int64, serverID string, refresh store.RefreshFunc) (store.MCPOAuthLink, error)
	SetStatus(ctx context.Context, userID int64, serverID, status string) error
	SetStatusIfVersion(ctx context.Context, userID int64, serverID, status string, casVersion int64) error
	Delete(ctx context.Context, userID int64, serverID string) error
	CreateTransaction(ctx context.Context, t store.MCPOAuthTransaction) error
	ConsumeTransaction(ctx context.Context, stateHash []byte, userID int64, bindingHash []byte, now time.Time) (store.MCPOAuthTransaction, error)
	DeleteExpiredTransactions(ctx context.Context, userID int64, now time.Time) error
	CountTransactions(ctx context.Context, userID int64, now time.Time) (int, error)
}

// LinkState is what a user can see about one integration. It deliberately
// carries no token material.
type LinkState struct {
	ServerID        string
	Linked          bool
	Status          string
	Scope           string
	AccessExpiresAt time.Time
}

// Service drives the browser authorization flow and keeps each user's grant
// usable afterwards.
type Service struct {
	store       LinkStore
	clients     map[string]*Client
	scopes      map[string][]string
	redirectURI string
	now         func() time.Time
}

// NewService wires the service. clients and scopes are keyed by the server's
// integration id.
func NewService(
	st LinkStore, clients map[string]*Client, redirectURI string,
	scopes map[string][]string, now func() time.Time,
) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: st, clients: clients, scopes: scopes, redirectURI: redirectURI, now: now}
}

// Servers lists the configured integration ids, sorted so the UI order is
// stable.
func (s *Service) Servers() []string {
	out := make([]string, 0, len(s.clients))
	for id := range s.clients {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func digest(v string) []byte {
	sum := sha256.Sum256([]byte(v))
	return sum[:]
}

// Start opens an authorization: it returns the URL the browser must visit, the
// state it carries, and the browser token that must come back in a cookie.
func (s *Service) Start(ctx context.Context, userID int64, serverID string) (authorizeURL, state, browserToken string, err error) {
	client, ok := s.clients[serverID]
	if !ok {
		return "", "", "", fmt.Errorf("%w: %s", ErrUnknownServer, serverID)
	}

	now := s.now()
	if err := s.store.DeleteExpiredTransactions(ctx, userID, now); err != nil {
		return "", "", "", err
	}
	open, err := s.store.CountTransactions(ctx, userID, now)
	if err != nil {
		return "", "", "", err
	}
	if open >= maxOpenTransactions {
		return "", "", "", ErrTooManyAttempts
	}

	pkce, err := NewPKCE()
	if err != nil {
		return "", "", "", err
	}
	state, err = NewState()
	if err != nil {
		return "", "", "", err
	}
	browserToken, err = NewState()
	if err != nil {
		return "", "", "", err
	}

	if err := s.store.CreateTransaction(ctx, store.MCPOAuthTransaction{
		StateHash:    digest(state),
		UserID:       userID,
		ServerID:     serverID,
		PKCEVerifier: pkce.Verifier,
		RedirectURI:  s.redirectURI,
		BindingHash:  digest(browserToken),
		ExpiresAt:    now.Add(transactionTTL),
	}); err != nil {
		return "", "", "", err
	}

	return client.AuthorizeURL(s.redirectURI, state, pkce.Challenge, s.scopes[serverID]), state, browserToken, nil
}

// Complete finishes an authorization begun by Start.
//
// The transaction is consumed by a statement that also matches the owner and
// the browser binding, so a caller holding only a state value cannot delete
// someone else's pending authorization.
func (s *Service) Complete(ctx context.Context, userID int64, code, state, browserToken string) (string, error) {
	tx, err := s.store.ConsumeTransaction(ctx, digest(state), userID, digest(browserToken), s.now())
	if errors.Is(err, store.ErrTransactionNotFound) {
		return "", ErrBadTransaction
	}
	if err != nil {
		return "", err
	}

	client, ok := s.clients[tx.ServerID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownServer, tx.ServerID)
	}

	tokens, err := client.Exchange(ctx, code, tx.PKCEVerifier, tx.RedirectURI)
	if err != nil {
		return "", err
	}

	if err := s.store.Upsert(ctx, store.MCPOAuthLink{
		UserID:          userID,
		ServerID:        tx.ServerID,
		AccessToken:     tokens.AccessToken,
		AccessExpiresAt: s.now().Add(tokens.ExpiresIn),
		RefreshToken:    tokens.RefreshToken,
		Scope:           tokens.Scope,
		Resource:        client.Metadata().Resource,
		Status:          store.LinkStatusLinked,
	}); err != nil {
		return "", err
	}
	return tx.ServerID, nil
}

// TokenFor returns a live access token for this user and server, refreshing
// under the row lock when the stored one is close to expiring.
func (s *Service) TokenFor(ctx context.Context, userID int64, serverID string) (string, error) {
	link, err := s.store.Get(ctx, userID, serverID)
	switch {
	case errors.Is(err, store.ErrLinkNotFound):
		return "", ErrNotLinked
	case err != nil:
		return "", err
	case link.Status == store.LinkStatusReauthRequired:
		return "", ErrReauthRequired
	case link.Status != store.LinkStatusLinked:
		return "", ErrNotLinked
	case s.now().Before(link.AccessExpiresAt.Add(-refreshSkew)):
		return link.AccessToken, nil
	}

	client, ok := s.clients[serverID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownServer, serverID)
	}

	rotated, err := s.store.RotateUnderLock(ctx, userID, serverID,
		func(ctx context.Context, current store.MCPOAuthLink) (store.MCPOAuthLink, error) {
			// A peer may have refreshed while this caller waited for the row.
			if s.now().Before(current.AccessExpiresAt.Add(-refreshSkew)) {
				return current, nil
			}
			tokens, rErr := client.Refresh(ctx, current.RefreshToken)
			if rErr != nil {
				return store.MCPOAuthLink{}, rErr
			}
			next := current
			next.AccessToken = tokens.AccessToken
			next.RefreshToken = tokens.RefreshToken
			next.AccessExpiresAt = s.now().Add(tokens.ExpiresIn)
			if tokens.Scope != "" {
				next.Scope = tokens.Scope
			}
			return next, nil
		})

	switch {
	case errors.Is(err, ErrInvalidGrant), errors.Is(err, store.ErrRotationUncertain):
		// The presented refresh token is spent either way, and replaying it
		// would revoke the family. Condemn the link — but only while it is
		// still the one this caller read, so a user who re-authorized in the
		// meantime keeps a healthy link.
		if setErr := s.store.SetStatusIfVersion(ctx, userID, serverID,
			store.LinkStatusReauthRequired, link.CASVersion); setErr != nil &&
			!errors.Is(setErr, store.ErrCASConflict) {
			return "", fmt.Errorf("oauth: mark reauth required: %w", setErr)
		}
		return "", ErrReauthRequired
	case errors.Is(err, store.ErrLinkNotUsable):
		return "", ErrReauthRequired
	case errors.Is(err, store.ErrLinkNotFound):
		return "", ErrNotLinked
	case err != nil:
		return "", err
	}
	return rotated.AccessToken, nil
}

// Unlink revokes the grant upstream and then removes the local copy.
//
// A failed revocation does not block the removal: the user asked to
// disconnect, and keeping their tokens because a remote call failed serves
// nobody. Deleting the local copy revokes nothing at Garmin itself, which is
// what the UI must say.
func (s *Service) Unlink(ctx context.Context, userID int64, serverID string) error {
	link, err := s.store.Get(ctx, userID, serverID)
	if errors.Is(err, store.ErrLinkNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if client, ok := s.clients[serverID]; ok {
		if revokeErr := client.Revoke(ctx, link.RefreshToken); revokeErr != nil {
			slog.Warn("mcp oauth: upstream revocation failed; removing the local link anyway",
				"server", serverID, "error", revokeErr)
			if statusErr := s.store.SetStatus(ctx, userID, serverID,
				store.LinkStatusDisconnectPending); statusErr != nil {
				slog.Warn("mcp oauth: recording disconnect_pending failed", "error", statusErr)
			}
		}
	}
	return s.store.Delete(ctx, userID, serverID)
}

// Integrations reports one state per configured server.
func (s *Service) Integrations(ctx context.Context, userID int64) ([]LinkState, error) {
	ids := s.Servers()
	out := make([]LinkState, 0, len(ids))
	for _, id := range ids {
		link, err := s.store.Get(ctx, userID, id)
		switch {
		case errors.Is(err, store.ErrLinkNotFound):
			out = append(out, LinkState{ServerID: id})
			continue
		case err != nil:
			return nil, err
		}
		out = append(out, LinkState{
			ServerID:        id,
			Linked:          true,
			Status:          link.Status,
			Scope:           link.Scope,
			AccessExpiresAt: link.AccessExpiresAt,
		})
	}
	return out, nil
}
