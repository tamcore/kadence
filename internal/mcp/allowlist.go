package mcp

import (
	"fmt"
	"net/url"
	"strings"
)

// HostAllowed validates that rawURL is https and its host matches one of the
// allowlist patterns. A pattern is either an exact host or a "*." wildcard that
// matches one-or-more leading labels (e.g. "*.foo.io" matches "a.foo.io" and
// "a.b.foo.io", but not "foo.io"). Empty patterns deny everything.
func HostAllowed(rawURL string, patterns []string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("URL must use https (got %q)", u.Scheme)
	}
	host := u.Hostname() // strips port
	if host == "" {
		return fmt.Errorf("URL has no host")
	}
	for _, p := range patterns {
		if hostMatches(host, p) {
			return nil
		}
	}
	return fmt.Errorf("host %q is not in the allowed list", host)
}

func hostMatches(host, pattern string) bool {
	host = strings.ToLower(host)
	pattern = strings.ToLower(pattern)
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}
