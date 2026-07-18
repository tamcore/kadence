package middleware

import (
	"net/http"

	"github.com/gorilla/csrf"
)

// CSRF returns gorilla/csrf protection. The token is surfaced on every response
// via the X-CSRF-Token header so the SPA can read and echo it back.
func CSRF(secret []byte, secure bool) func(http.Handler) http.Handler {
	protect := csrf.Protect(
		secret,
		csrf.Secure(secure),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)
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
