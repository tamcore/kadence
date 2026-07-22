package mcp

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// serverNamePattern constrains user-defined MCP server names: lowercase
// alphanumeric, hyphen-separated, max 32 chars. It deliberately excludes
// "_" entirely, so a server name can never collide with the "__" separator
// the registry uses to namespace tool names ("<server>__<tool>").
var serverNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidateServerName reports an error unless name matches serverNamePattern.
func ValidateServerName(name string) error {
	if !serverNamePattern.MatchString(name) {
		return fmt.Errorf("name must match %s", serverNamePattern.String())
	}
	return nil
}

// ValidateTransport reports an error unless transport is one of the
// recognized Server.Transport values (streamable-http, sse).
func ValidateTransport(transport string) error {
	switch transport {
	case transportStreamableHTTP, transportSSE:
		return nil
	default:
		return fmt.Errorf("transport must be %q or %q", transportStreamableHTTP, transportSSE)
	}
}

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
