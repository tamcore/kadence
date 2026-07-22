package middleware

import (
	"net/http"
	"strings"
)

// devCSP is the fallback Content-Security-Policy used whenever the strict,
// hash-based policy can't be safely constructed: dev builds (no embedded
// frontend), and the degenerate case of a prod build whose embedded frontend
// is missing its csp-hashes.json (see web.CSPScriptHashes). It intentionally
// allows 'unsafe-inline' scripts so the Vite dev server / an unhashed build
// keeps working, rather than shipping a strict script-src with zero hashes
// that would silently break the SPA's own inline bootstrap script.
const devCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// hstsHeader is the Strict-Transport-Security value applied in production.
const hstsHeader = "max-age=31536000; includeSubDomains"

// SecurityHeaders sets baseline security response headers on every response,
// plus a Content-Security-Policy and (in production) HSTS.
//
// scriptHashes are the "sha256-<base64>" CSP hash-source tokens for the
// embedded frontend's inline scripts (web.CSPScriptHashes()); when non-empty,
// the response gets the strict, hash-based policy. When empty — dev builds,
// or a prod build missing its hashes file — the response gets devCSP instead,
// so a hash-pipeline failure degrades to a permissive policy rather than a
// broken SPA.
func SecurityHeaders(isProd bool, scriptHashes []string) func(http.Handler) http.Handler {
	csp := devCSP
	if len(scriptHashes) > 0 {
		csp = strictCSP(scriptHashes)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Content-Security-Policy", csp)
			if isProd {
				h.Set("Strict-Transport-Security", hstsHeader)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// strictCSP builds the hash-based script-src policy from the given
// "sha256-<base64>" tokens.
func strictCSP(scriptHashes []string) string {
	quoted := make([]string, len(scriptHashes))
	for i, hash := range scriptHashes {
		quoted[i] = "'" + hash + "'"
	}
	scriptSrc := "script-src 'self' " + strings.Join(quoted, " ")
	return "default-src 'self'; " + scriptSrc + "; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
		"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
}
