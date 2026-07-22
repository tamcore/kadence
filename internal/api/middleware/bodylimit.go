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
	return func(next http.Handler) http.Handler {
		if max <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}
