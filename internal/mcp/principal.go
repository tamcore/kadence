package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

// principalFor returns the cache/credential principal for a dispatch to s on
// behalf of username. A server that shares one credential needs none, so it
// returns the empty string; a per-principal server resolves the immutable
// user id.
func (r *Registry) principalFor(ctx context.Context, s Server, username string) (string, error) {
	if !s.PerPrincipal() {
		return "", nil
	}
	src := r.principalSource()
	if src == nil {
		return "", fmt.Errorf("%w for server %s/%s", errNoPrincipalSource, s.Name, s.Scope)
	}
	id, err := src.UserIDFor(ctx, username)
	if err != nil {
		return "", fmt.Errorf("mcp: resolve principal for %s/%s: %w", s.Name, s.Scope, err)
	}
	return strconv.FormatInt(id, 10), nil
}

// clientCacheKey is a client's cache identity. A per-principal server keys on
// the principal as well, because its credential is that one user's: sharing
// the entry would share the credential.
func clientCacheKey(s Server, principal string) string {
	key := s.Name + "/" + s.Scope
	if s.PerPrincipal() {
		key += "/" + principal
	}
	return key
}
