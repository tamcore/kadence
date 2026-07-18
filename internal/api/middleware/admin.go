package middleware

import (
	"net/http"

	"github.com/tamcore/kadence/internal/auth"
)

// RequireAdmin rejects non-admin requests (403). Assumes LoadUser + RequireAuth ran.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil || !u.IsAdmin() {
			writeJSONError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
