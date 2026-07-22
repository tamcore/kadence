package middleware

import "net/http"

// MaxBodyBytes caps the request body at max bytes using http.MaxBytesReader.
// max <= 0 disables the cap (the returned middleware is a no-op passthrough),
// matching the "0 disables" convention used elsewhere in this package (see
// RateLimit). Handlers that decode a capped body (json.Decoder, ParseForm,
// io.ReadAll, ...) get a *http.MaxBytesError on overflow; existing decode-error
// handling across the API already maps any body-read/decode failure to 400,
// so no handler changes are needed for this to take effect.
func MaxBodyBytes(max int64) func(http.Handler) http.Handler {
	return MaxBodyBytesExempt(max, nil)
}

// MaxBodyBytesExempt behaves like MaxBodyBytes, except requests for which
// exempt returns true pass through with the body untouched.
//
// This exists so a single global body-size cap can sit in front of the whole
// authed route tree while letting specific routes (e.g. document uploads,
// which apply their own larger cap at the handler level) opt out — without
// nesting two http.MaxBytesReaders around the same body, where the smaller of
// the two limits always wins regardless of which one is "supposed" to apply.
// exempt == nil behaves exactly like MaxBodyBytes (no exemptions).
func MaxBodyBytesExempt(max int64, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if max <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt != nil && exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}
