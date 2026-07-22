package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/httprate"

	"github.com/tamcore/kadence/internal/api/handlers"
)

// RateLimit returns per-IP sliding-window rate-limiting middleware allowing
// up to limit requests per minute. limit <= 0 disables rate limiting (the
// returned middleware is a no-op passthrough), matching the "0 disables"
// convention used by KADENCE_RATE_LIMIT_GLOBAL / KADENCE_RATE_LIMIT_AUTH.
//
// The rate-limit key is the request's RemoteAddr (httprate.KeyByIP), which is
// safe here because chi's RealIP middleware runs earlier in the chain
// (router.go) and rewrites RemoteAddr from the trusted reverse proxy's
// X-Forwarded-For/X-Real-IP header before this middleware ever sees the
// request. Deployments that expose the server directly (no trusted proxy in
// front) must not forward client-supplied XFF/X-Real-IP headers, or clients
// could spoof their rate-limit bucket.
func RateLimit(limit int) func(http.Handler) http.Handler {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	return httprate.LimitBy(limit, time.Minute,
		httprate.KeyByIP, //nolint:staticcheck // deprecated but intentional; see doc comment above
		httprate.WithLimitHandler(rateLimitExceeded),
	)
}

// rateLimitExceeded writes the standard error envelope for a 429 response.
// The Retry-After header is already set by httprate before this handler
// runs (httprate always sets it on limit, regardless of a custom handler).
func rateLimitExceeded(w http.ResponseWriter, _ *http.Request) {
	handlers.RespondError(w, http.StatusTooManyRequests, "too many requests")
}
