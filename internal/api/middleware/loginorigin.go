package middleware

import (
	"net/http"
	"slices"
	"strings"
)

// jsonContentType is the only body type the credential endpoints accept.
const jsonContentType = "application/json"

// RequireLoginOrigin guards the endpoints that create a session before any CSRF
// token can exist.
//
// gorilla/csrf cannot protect login: the victim has no token yet, which is the
// point of the request. Without a guard, a page on any origin can submit a form
// that logs the victim's browser into an account the attacker controls, and
// everything the victim then connects — a Garmin account, say — is connected to
// the attacker's account instead.
//
// Two checks close it, and a cross-site form fails both:
//
//   - the body must be declared application/json, which an HTML form cannot
//     send (it is limited to three simple content types, and a fetch that sets
//     it triggers a preflight the browser will not complete without CORS);
//   - a request that carries an Origin must carry one this deployment trusts.
//
// A non-browser client sends no Origin and is unaffected.
func RequireLoginOrigin(trustedOrigins []string, isProd bool) func(http.Handler) http.Handler {
	allowed := make([]string, 0, len(trustedOrigins))
	for _, o := range trustedOrigins {
		if trimmed := strings.TrimRight(strings.TrimSpace(o), "/"); trimmed != "" {
			allowed = append(allowed, trimmed)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mediaType(r.Header.Get("Content-Type")) != jsonContentType {
				http.Error(w, "login requires a JSON request body", http.StatusUnsupportedMediaType)
				return
			}
			if origin := strings.TrimRight(r.Header.Get("Origin"), "/"); origin != "" &&
				!originAllowed(origin, allowed, r, isProd) {
				http.Error(w, "cross-origin login is refused", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originAllowed accepts a configured trusted origin, and otherwise the
// deployment's own origin as the request reports it. Outside production the
// scheme is http, which is why the comparison is built rather than fixed.
func originAllowed(origin string, allowed []string, r *http.Request, isProd bool) bool {
	if slices.Contains(allowed, origin) {
		return true
	}
	scheme := "http"
	if isProd || r.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}

// mediaType strips parameters and case from a Content-Type header.
func mediaType(header string) string {
	base, _, _ := strings.Cut(header, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
