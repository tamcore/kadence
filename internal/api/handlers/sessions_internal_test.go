package handlers

import "testing"

func TestParseUserAgent(t *testing.T) {
	if d := parseUserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/537 (KHTML) Chrome/120 Safari/537"); d != "Chrome on macOS" {
		t.Fatalf("got %q", d)
	}
	if d := parseUserAgent(""); d != "Unknown device" {
		t.Fatalf("empty got %q", d)
	}
}
