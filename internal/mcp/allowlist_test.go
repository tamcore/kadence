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
