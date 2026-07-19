package middleware

import (
	"net/http"

	"github.com/gorilla/csrf"
)

// CSRF returns gorilla/csrf protection. The token is surfaced on every response
// via the X-CSRF-Token header so the SPA can read and echo it back.
func CSRF(secret []byte, secure bool, trustedOrigins []string) func(http.Handler) http.Handler {
	baseOpts := []csrf.Option{
		csrf.Secure(secure),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	}
	if len(trustedOrigins) > 0 {
		baseOpts = append(baseOpts, csrf.TrustedOrigins(trustedOrigins))
	}
	protect := csrf.Protect(secret, baseOpts...)
	expose := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-CSRF-Token", csrf.Token(r))
			next.ServeHTTP(w, r)
		})
	}
	return func(next http.Handler) http.Handler {
		return protect(expose(next))
	}
}
