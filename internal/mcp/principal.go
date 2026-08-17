package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// errNoPrincipalSource is returned when a server needs a per-user credential
// but nothing can resolve the user to a stable identity.
var errNoPrincipalSource = errors.New("mcp: no principal source configured")

// ErrProbeNeedsPrincipal is returned by Registry.Probe for a per-principal
// server. Probing has no user, and a server whose credential is per user has no
// deployment-wide credential to probe with, so dialing it would only ever
// produce an unauthenticated failure.
//
// The health poller currently records this as unhealthy for every user, which
// is wrong but honest: it says "this server was not verified" rather than
// claiming it works. Splitting deployment liveness from each user's link state
// belongs to the phase that introduces link state.
var ErrProbeNeedsPrincipal = errors.New("mcp: server is per-principal and cannot be probed without a user")

// PrincipalSource maps a username to the immutable user id a per-user
// credential is filed under. A username can be changed by an admin, so it is
// not usable as the identity a cached client or a stored token is keyed by.
type PrincipalSource interface {
	UserIDFor(ctx context.Context, username string) (int64, error)
}

// SetPrincipalSource installs the resolver used for per-principal servers.
// Servers with AuthMode oauth are unusable until one is set.
func (r *Registry) SetPrincipalSource(src PrincipalSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.principals = src
}

func (r *Registry) principalSource() PrincipalSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.principals
}

// errNoTokenSource is returned when a per-principal server has no way to obtain
// a user's bearer token.
var errNoTokenSource = errors.New("mcp: no token source configured")

// TokenSource hands out a live access token for one user's authorization with
// one server. A failure means that user cannot use the server right now —
// usually because they have not linked it, or must link it again.
type TokenSource interface {
	TokenFor(ctx context.Context, userID int64, serverID string) (string, error)
}

// SetTokenSource installs the provider of per-user bearer tokens.
func (r *Registry) SetTokenSource(src TokenSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens = src
}

func (r *Registry) tokenSource() TokenSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens
}

// principalAuth is the identity and the credential for one dispatch. They
// travel together so a cached client can never be handed a bearer that belongs
// to a different principal.
type principalAuth struct {
	principal string
	bearer    string
}

// authFor resolves the identity and credential for a dispatch to s on behalf of
// username. A server that shares one credential needs neither, so it returns
// the zero value; a per-principal server resolves the immutable user id and
// that user's own token.
func (r *Registry) authFor(ctx context.Context, s Server, username string) (principalAuth, error) {
	if !s.PerPrincipal() {
		return principalAuth{}, nil
	}
	src := r.principalSource()
	if src == nil {
		return principalAuth{}, fmt.Errorf("%w for server %s/%s", errNoPrincipalSource, s.Name, s.Scope)
	}
	id, err := src.UserIDFor(ctx, username)
	if err != nil {
		return principalAuth{}, fmt.Errorf("mcp: resolve principal for %s/%s: %w", s.Name, s.Scope, err)
	}

	tokens := r.tokenSource()
	if tokens == nil {
		return principalAuth{}, fmt.Errorf("%w for server %s/%s", errNoTokenSource, s.Name, s.Scope)
	}
	bearer, err := tokens.TokenFor(ctx, id, s.IntegrationID())
	if err != nil {
		return principalAuth{}, fmt.Errorf("mcp: no usable authorization for %s/%s: %w", s.Name, s.Scope, err)
	}
	return principalAuth{principal: strconv.FormatInt(id, 10), bearer: bearer}, nil
}

// validateOAuth reports whether an oauth server carries the client identity it
// needs. The resource is required because the authorization server refuses an
// authorization request whose resource indicator is not exactly its own.
func (s Server) validateOAuth() error {
	if !s.PerPrincipal() {
		return nil
	}
	if strings.TrimSpace(s.OAuthClientID) == "" {
		return fmt.Errorf("mcp: server %s/%s: oauth needs a client id", s.Name, s.Scope)
	}
	if strings.TrimSpace(s.OAuthResource) == "" {
		return fmt.Errorf("mcp: server %s/%s: oauth needs a resource", s.Name, s.Scope)
	}
	// The authorization server compares the resource indicator exactly, and the
	// only resource it serves is its own MCP endpoint — this server's URL.
	if s.OAuthResource != s.URL {
		return fmt.Errorf("mcp: server %s/%s: oauth resource must equal the server URL", s.Name, s.Scope)
	}
	// Every request to this server carries a user's bearer token, so cleartext
	// would publish one user's credential per call. Basic-auth servers may stay
	// plaintext in-cluster; this one may not.
	if !strings.HasPrefix(s.URL, "https://") {
		return fmt.Errorf("mcp: server %s/%s: an oauth server must be https, it carries per-user bearer tokens", s.Name, s.Scope)
	}
	if len(s.OAuthScopes) == 0 {
		return fmt.Errorf("mcp: server %s/%s: oauth needs at least one scope", s.Name, s.Scope)
	}
	for _, scope := range s.OAuthScopes {
		if !grantableScopes[scope] {
			return fmt.Errorf("mcp: server %s/%s: scope %q is not grantable yet", s.Name, s.Scope, scope)
		}
	}
	return nil
}

// The garmin MCP server's tool tiers, as scope names.
const (
	ScopeGarminRead = "garmin:read"
	// ScopeGarminWrite gates the write tier. A granted scope is only half the
	// gate: the deployment must also enable the tier.
	ScopeGarminWrite = "garmin:write"
)

// grantableScopes bounds what Kadence may ask for. The destructive tier
// additionally requires an interactive confirmation the client must answer
// mid-call, which does not exist yet, so a configuration naming it is refused
// rather than quietly requested and then refused on every call.
var grantableScopes = map[string]bool{ScopeGarminRead: true, ScopeGarminWrite: true}

// IntegrationID is the stable public identifier of this server in URLs, API
// payloads, and the sealed-record context. It is the lowercased name, resolved
// in exactly one place so no call site lowercases ad hoc.
func (s Server) IntegrationID() string { return strings.ToLower(s.Name) }

// clientCacheKey is a client's cache identity. A per-principal server keys on
// the principal as well, because its credential is that one user's: sharing the
// entry would share the credential.
//
// It also keys on the credential itself, through a digest. The transport fixes
// the Authorization header when it is dialed, so a client cached before a
// refresh would keep presenting the token it was built with — every call
// failing with 401 until the entry happened to be evicted. Keying on the
// credential retires that client the moment the token rotates.
func clientCacheKey(s Server, auth principalAuth) string {
	key := s.Name + "/" + s.Scope
	if !s.PerPrincipal() {
		return key
	}
	key += "/" + auth.principal
	if auth.bearer != "" {
		sum := sha256.Sum256([]byte(auth.bearer))
		key += "/" + hex.EncodeToString(sum[:8])
	}
	return key
}
