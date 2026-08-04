package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
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

var errRemote = errors.New("remote failure")

// TestBoundErrorCapsTheRenderedMessage covers the transport-failure path: the
// wrapped error can carry an unbounded remote response body, which the chat
// service turns into a tool result and sends to the model.
func TestBoundErrorCapsTheRenderedMessage(t *testing.T) {
	huge := fmt.Errorf("mcp: call tool t: %w: %s", errRemote, strings.Repeat("x", maxToolResultBytes+4096))

	bounded := boundError(huge)

	if len(bounded.Error()) > maxToolResultBytes+len(toolResultTruncatedMarker) {
		t.Fatalf("bounded error message is %d bytes, want at most %d",
			len(bounded.Error()), maxToolResultBytes+len(toolResultTruncatedMarker))
	}
	if !strings.HasSuffix(bounded.Error(), toolResultTruncatedMarker) {
		t.Fatal("bounded error message missing the truncation marker")
	}
	if !errors.Is(bounded, errRemote) {
		t.Fatal("bounding an error broke the %w chain")
	}
}

func TestBoundErrorLeavesSmallErrorsIdentical(t *testing.T) {
	err := fmt.Errorf("mcp: call tool t: %w", errRemote)

	if bounded := boundError(err); bounded != err {
		t.Fatalf("boundError returned a new error for a small message: %v", bounded)
	}
	if boundError(nil) != nil {
		t.Fatal("boundError(nil) must stay nil")
	}
}

// TestCapToolSchemaReplacesAnOversizedSchema pins the ListTools side of the cap:
// a remote server's schema is model input too, so an unbounded one cannot be
// forwarded. It must stay valid JSON, so it is replaced rather than cut.
func TestCapToolSchemaReplacesAnOversizedSchema(t *testing.T) {
	huge := json.RawMessage(`{"type":"object","description":"` + strings.Repeat("x", maxToolSchemaBytes) + `"}`)

	capped := capToolSchema("t", huge)

	if len(capped) > maxToolSchemaBytes {
		t.Fatalf("capped schema is %d bytes, want at most %d", len(capped), maxToolSchemaBytes)
	}
	if !json.Valid(capped) {
		t.Fatalf("capped schema is not valid JSON: %s", capped)
	}
}

func TestCapToolSchemaKeepsASmallSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)

	if capped := capToolSchema("t", schema); string(capped) != string(schema) {
		t.Fatalf("capToolSchema changed a small schema: %s", capped)
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
