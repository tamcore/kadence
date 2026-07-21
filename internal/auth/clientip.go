package auth

import (
	"net"
	"net/http"
)

// ClientIP returns the client IP from r.RemoteAddr, stripping any :port.
// RealIP middleware runs earlier, so RemoteAddr is already the real client IP.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // no port present
	}
	return host
}
