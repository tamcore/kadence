// Package mcp implements a registry of remote Model Context Protocol (MCP)
// servers, configured entirely via environment variables. Servers are
// grouped by name and scope (global or per-user), listed for tool
// discovery, and dispatched to for tool calls. Network transport only
// (streamable-http / sse) — no stdio, no in-process registry, no
// Kubernetes dependency.
package mcp

import (
	"log/slog"
	"strings"
)

const (
	envPrefix = "MCP_"

	// scopeGlobal is the Scope value applying a server to every user.
	scopeGlobal = "GLOBAL"
	// userScopePrefix precedes a username in a per-user Scope value.
	userScopePrefix = "USER_"
)

// field is a known env-var suffix identifying which Server attribute a
// MCP_<NAME>_<SCOPE>_<FIELD> variable sets.
type field int

const (
	fieldURL field = iota
	fieldTransport
	fieldAuthUser
	fieldAuthPass
	fieldTools
)

// knownFieldSuffixes lists the env-var suffixes recognized after
// MCP_<NAME>_<SCOPE>. Order matters: longer/more-specific suffixes
// (_AUTH_USER, _AUTH_PASS — two tokens) must be checked before matching on
// a single trailing token, so they aren't mis-split.
var knownFieldSuffixes = []struct {
	suffix string
	field  field
}{
	{"_AUTH_USER", fieldAuthUser},
	{"_AUTH_PASS", fieldAuthPass},
	{"_TRANSPORT", fieldTransport},
	{"_URL", fieldURL},
	{"_TOOLS", fieldTools},
}

// Server describes one remote MCP server derived from the env contract.
type Server struct {
	Name      string // e.g. "GARMIN"
	Scope     string // "GLOBAL" or "USER_<username>"
	URL       string
	AuthUser  string
	AuthPass  string
	Transport string // "streamable-http" | "sse"
	// Tools is an app-side allowlist of glob patterns (path.Match syntax)
	// matched against the unprefixed tool name. Empty/nil means no
	// filtering — all tools the server exposes are allowed.
	Tools []string
}

// AppliesTo reports whether this server's tools should be offered to the
// given username: true for GLOBAL-scoped servers, or for USER_<username>
// servers matching exactly.
func (s Server) AppliesTo(username string) bool {
	return s.Scope == scopeGlobal || s.Scope == userScopePrefix+username
}

// serverBuilder accumulates fields for one (Name, Scope) group while
// scanning the environment, before being finalized into a Server.
type serverBuilder struct {
	name, scope    string
	url, transport string
	authUser       string
	authPass       string
	tools          string
	hasURL         bool
}

// ServersFromEnv parses MCP_<NAME>_<SCOPE>_<FIELD> environment entries
// (in "KEY=VALUE" form, as from os.Environ()) into a slice of Server.
// Non-MCP_ keys and unrecognized fields are ignored. A (Name, Scope) group
// missing a URL is skipped with a logged warning — one malformed server
// definition must not prevent startup.
func ServersFromEnv(environ []string) ([]Server, error) {
	groups := map[string]*serverBuilder{}
	var order []string

	for _, kv := range environ {
		groupKey, matchedField, value, ok := parseMCPEnvVar(kv)
		if !ok {
			continue
		}

		b, exists := groups[groupKey]
		if !exists {
			name, scope, _ := splitNameScope(groupKey)
			b = &serverBuilder{name: name, scope: scope}
			groups[groupKey] = b
			order = append(order, groupKey)
		}
		applyField(b, matchedField, value)
	}

	servers := make([]Server, 0, len(order))
	for _, groupKey := range order {
		b := groups[groupKey]
		if !b.hasURL {
			slog.Warn("mcp: skipping server with no URL", "server", groupKey)
			continue
		}
		servers = append(servers, Server{
			Name:      b.name,
			Scope:     b.scope,
			URL:       b.url,
			AuthUser:  b.authUser,
			AuthPass:  b.authPass,
			Transport: b.transport,
			Tools:     splitTools(b.tools),
		})
	}
	return servers, nil
}

// parseMCPEnvVar parses one "KEY=VALUE" env entry. It returns the group key
// ("<NAME>_<SCOPE>"), the matched field, the value, and whether the entry
// was a recognized MCP_ variable.
func parseMCPEnvVar(kv string) (groupKey string, matched field, value string, ok bool) {
	key, val, hasEq := strings.Cut(kv, "=")
	if !hasEq || !strings.HasPrefix(key, envPrefix) {
		return "", 0, "", false
	}
	rest := strings.TrimPrefix(key, envPrefix)

	matchedField, middle, matchedSuffix := matchFieldSuffix(rest)
	if !matchedSuffix {
		return "", 0, "", false
	}
	if _, _, validNameScope := splitNameScope(middle); !validNameScope {
		return "", 0, "", false
	}
	return middle, matchedField, val, true
}

// applyField sets the field on the builder identified by matchedField.
func applyField(b *serverBuilder, matchedField field, value string) {
	switch matchedField {
	case fieldURL:
		b.url = value
		b.hasURL = true
	case fieldTransport:
		b.transport = value
	case fieldAuthUser:
		b.authUser = value
	case fieldAuthPass:
		b.authPass = value
	case fieldTools:
		b.tools = value
	}
}

// splitTools parses a comma-separated MCP_<NAME>_<SCOPE>_TOOLS value into a
// trimmed, non-empty list of glob patterns. Empty input → nil (no filtering).
func splitTools(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for p := range strings.SplitSeq(raw, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// matchFieldSuffix finds the known field suffix at the end of rest (the env
// key with the MCP_ prefix already stripped) and returns the field, the
// remaining middle portion (<NAME>_<SCOPE>), and whether a match was found.
func matchFieldSuffix(rest string) (field, string, bool) {
	for _, known := range knownFieldSuffixes {
		if middle, ok := strings.CutSuffix(rest, known.suffix); ok {
			return known.field, middle, true
		}
	}
	return 0, "", false
}

// splitNameScope splits "<NAME>_<SCOPE>" into NAME (first token) and SCOPE
// (the remainder: "GLOBAL" or "USER_<username>").
func splitNameScope(middle string) (name, scope string, ok bool) {
	name, scope, found := strings.Cut(middle, "_")
	if !found || name == "" || scope == "" {
		return "", "", false
	}
	return name, scope, true
}
