package mcp_test

import (
	"testing"

	"github.com/tamcore/kadence/internal/mcp"
)

func TestHostAllowed(t *testing.T) {
	patterns := []string{"a.example.io", "*.foo.io"}
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://a.example.io/mcp", true},
		{"https://x.foo.io/mcp", true},
		{"https://x.y.foo.io/mcp", true},
		{"https://a.example.io:8443/mcp", true}, // port stripped
		{"https://foo.io/mcp", false},           // bare apex not matched by *.foo.io
		{"https://evil.com/mcp", false},
		{"http://a.example.io/mcp", false}, // must be https
		{"://bad", false},                  // unparseable / no scheme

		// SSRF wildcard dot-boundary regressions: "*.foo.io" must only match
		// "foo.io" plus a dot, never a mere string suffix.
		{"https://evilfoo.io/mcp", false}, // "evilfoo.io" has suffix "foo.io" but no dot boundary
		{"https://xfoo.io/mcp", false},    // same class of suffix-without-dot attack
		{"https://notfoo.io/mcp", false},  // same class of suffix-without-dot attack
		{"https://a.b.foo.io/mcp", true},  // multi-label under the wildcard is allowed

		// Userinfo trick: the real host is evil.com; url.Hostname() must ignore
		// the userinfo component entirely.
		{"https://a.example.io@evil.com/mcp", false},
	}
	for _, c := range cases {
		err := mcp.HostAllowed(c.url, patterns)
		if (err == nil) != c.ok {
			t.Errorf("HostAllowed(%q) err=%v, want ok=%v", c.url, err, c.ok)
		}
	}
}

func TestHostAllowedEmptyPatternsDeny(t *testing.T) {
	if mcp.HostAllowed("https://a.example.io", nil) == nil {
		t.Fatal("empty patterns should deny")
	}
}

func TestValidateHint(t *testing.T) {
	if err := mcp.ValidateHint(""); err != nil {
		t.Fatalf("empty hint should be valid, got %v", err)
	}
	ok := make([]rune, mcp.HintMaxLen)
	for i := range ok {
		ok[i] = 'a'
	}
	if err := mcp.ValidateHint(string(ok)); err != nil {
		t.Fatalf("hint at exactly HintMaxLen should be valid, got %v", err)
	}
	over := make([]rune, mcp.HintMaxLen+1)
	for i := range over {
		over[i] = 'a'
	}
	if err := mcp.ValidateHint(string(over)); err == nil {
		t.Fatal("hint over HintMaxLen should be invalid")
	}
}
