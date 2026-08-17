package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
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
	// condemnTimeout bounds the write that records a dead grant. It runs after
	// the caller's own context may already be gone.
	condemnTimeout = 5 * time.Second
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
	// ScopeShortfall names the configured scopes this grant does not carry,
	// which happens when the deployment starts asking for a wider tier than the
	// user authorized. A refresh can never widen scope, so the only remedy is a
	// fresh authorization — and until then every call in the missing tier is
	// refused by the server.
	ScopeShortfall []string
}

// Discoverer builds a client for a server whose metadata is not known yet. It
// exists so an integration whose server was unreachable at boot recovers on its
// own rather than staying dead until the next restart.
type Discoverer func(ctx context.Context, serverID string) (*Client, error)

// Service drives the browser authorization flow and keeps each user's grant
// usable afterwards.
type Service struct {
	store       LinkStore
	scopes      map[string][]string
	redirectURI string
	now         func() time.Time

	mu         sync.Mutex
	clients    map[string]*Client
	discoverer Discoverer
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
	if clients == nil {
		clients = map[string]*Client{}
	}
	return &Service{store: st, clients: clients, scopes: scopes, redirectURI: redirectURI, now: now}
}

// SetDiscoverer installs the retry path for an integration whose metadata could
// not be read yet.
func (s *Service) SetDiscoverer(d Discoverer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoverer = d
}

// clientFor returns the client for serverID, discovering it if boot could not.
// A server that is configured but undiscoverable is reported as unknown: from a
// caller's point of view an integration that cannot be reached is one that
// cannot be used.
func (s *Service) clientFor(ctx context.Context, serverID string) (*Client, error) {
	s.mu.Lock()
	client, ok := s.clients[serverID]
	discoverer := s.discoverer
	s.mu.Unlock()
	if ok {
		return client, nil
	}
	if discoverer == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownServer, serverID)
	}

	discovered, err := discoverer(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnknownServer, serverID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// A peer may have discovered it first; keep one client per server.
	if existing, raced := s.clients[serverID]; raced {
		return existing, nil
	}
	s.clients[serverID] = discovered
	return discovered, nil
}

// Servers lists the configured integration ids, sorted so the UI order is
// stable.
func (s *Service) Servers() []string {
	out := make([]string, 0, len(s.scopes))
	for id := range s.scopes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// scopeShortfall returns the configured scopes a granted scope string does not
// carry. The granted string is space separated, as RFC 6749 defines it.
func scopeShortfall(configured []string, granted string) []string {
	have := map[string]bool{}
	for s := range strings.FieldsSeq(granted) {
		have[s] = true
	}
	var missing []string
	for _, want := range configured {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	return missing
}

func digest(v string) []byte {
	sum := sha256.Sum256([]byte(v))
	return sum[:]
}

// Start opens an authorization: it returns the URL the browser must visit, the
// state it carries, and the browser token that must come back in a cookie.
func (s *Service) Start(ctx context.Context, userID int64, serverID string) (authorizeURL, state, browserToken string, err error) {
	client, err := s.clientFor(ctx, serverID)
	if err != nil {
		return "", "", "", err
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

	client, err := s.clientFor(ctx, tx.ServerID)
	if err != nil {
		return "", err
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

	client, err := s.clientFor(ctx, serverID)
	if err != nil {
		return "", err
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
	case errors.Is(err, store.ErrLinkNotUsable):
		return "", ErrReauthRequired
	case errors.Is(err, store.ErrLinkNotFound):
		return "", ErrNotLinked
	case err != nil && refreshTokenIsSpent(err):
		// The token reached the server, or may have: it is spent either way,
		// and replaying it would revoke the family. Condemn the link.
		s.condemn(ctx, userID, serverID, link.CASVersion)
		return "", ErrReauthRequired
	case err != nil:
		// The request never left, so the stored token is untouched and the
		// caller may try again later.
		return "", err
	}
	return rotated.AccessToken, nil
}

// refreshTokenIsSpent reports whether a failed refresh may already have
// consumed the presented token.
//
// invalid_grant is certain. So is an uncertain persist. Everything else that is
// not a clean, named refusal from the server — a truncated body, a cancelled
// request, a read timeout — is treated as spent too, because the request may
// have arrived and the alternative is presenting a consumed token again, which
// destroys a family that might still be alive. The cost of being wrong here is
// one re-authorization; the cost of the other mistake is silent breakage.
func refreshTokenIsSpent(err error) bool {
	switch {
	case errors.Is(err, ErrInvalidGrant), errors.Is(err, store.ErrRotationUncertain):
		return true
	case errors.Is(err, ErrServerFault):
		// A named 5xx: the server answered, so it rejected the request rather
		// than processing it.
		return false
	default:
		return true
	}
}

// condemn marks the link as needing a fresh authorization, on a context the
// caller's own cancellation cannot abort: the token is already spent, and
// losing this write would leave the link looking healthy while every later
// refresh replays a dead token.
func (s *Service) condemn(ctx context.Context, userID int64, serverID string, casVersion int64) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), condemnTimeout)
	defer cancel()

	err := s.store.SetStatusIfVersion(writeCtx, userID, serverID, store.LinkStatusReauthRequired, casVersion)
	switch {
	case err == nil, errors.Is(err, store.ErrCASConflict):
		// A conflict means the user re-authorized meanwhile: the new link is
		// healthy and must not be condemned by this stale failure.
	default:
		slog.Error("mcp oauth: could not mark the link as needing reauthorization",
			"server", serverID, "err", err)
	}
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

	if client, cErr := s.clientFor(ctx, serverID); cErr == nil {
		if revokeErr := client.Revoke(ctx, link.RefreshToken); revokeErr != nil {
			// The local link goes either way: the user asked to disconnect, and
			// keeping their tokens because a remote call failed serves nobody.
			// The grant may stay alive upstream until it expires, which is why
			// the UI says "removed" rather than "revoked".
			slog.Warn("mcp oauth: upstream revocation failed; removing the local link anyway",
				"server", serverID, "error", revokeErr)
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
			ScopeShortfall:  scopeShortfall(s.scopes[id], link.Scope),
		})
	}
	return out, nil
}
