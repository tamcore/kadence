package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateToolResultLeavesSmallOutputAlone(t *testing.T) {
	in := "a short tool result"
	if got := truncateToolResult(in); got != in {
		t.Fatalf("truncateToolResult(%q) = %q, want it unchanged", in, got)
	}
}

func TestTruncateToolResultCapsAndMarksOversizedOutput(t *testing.T) {
	in := strings.Repeat("x", maxToolResultBytes+4096)
	got := truncateToolResult(in)
	if len(got) >= len(in) {
		t.Fatalf("output not truncated: len = %d, input len = %d", len(got), len(in))
	}
	if !strings.HasSuffix(got, toolResultTruncatedMarker) {
		t.Fatal("truncated output missing the truncation marker")
	}
	if len(got) > maxToolResultBytes+len(toolResultTruncatedMarker) {
		t.Fatalf("truncated output too long: %d", len(got))
	}
}

func TestTruncateToolResultDoesNotSplitARune(t *testing.T) {
	// Fill past the cap with 3-byte runes so a naive byte cut lands mid-rune.
	in := strings.Repeat("€", (maxToolResultBytes/3)+16)
	got := truncateToolResult(in)
	body := strings.TrimSuffix(got, toolResultTruncatedMarker)
	if !utf8.ValidString(body) {
		t.Fatal("truncation split a multi-byte rune; body is not valid UTF-8")
	}
}
