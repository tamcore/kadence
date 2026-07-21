package chat

import (
	"strings"
	"testing"
)

// TestUnitPromptLine verifies unitPromptLine returns the imperial sentence
// only for "imperial", falling back to metric for anything else (including
// empty/unknown values).
func TestUnitPromptLine(t *testing.T) {
	if l := unitPromptLine("imperial"); !strings.Contains(l, "miles") || !strings.Contains(l, "min/mile") {
		t.Fatalf("imperial line = %q", l)
	}
	for _, u := range []string{"metric", "", "bogus"} {
		l := unitPromptLine(u)
		if !strings.Contains(l, "kilometers") || !strings.Contains(l, "min/km") {
			t.Fatalf("unit %q line = %q, want metric", u, l)
		}
	}
}

// TestSystemPromptIncludesUnitLine verifies systemPrompt appends the correct
// unit-system sentence for the given unit preference.
func TestSystemPromptIncludesUnitLine(t *testing.T) {
	s := NewService(nil, ServiceConfig{}, Deps{})
	if !strings.Contains(s.systemPrompt("imperial"), "miles") {
		t.Fatal("imperial systemPrompt missing miles line")
	}
	if !strings.Contains(s.systemPrompt("metric"), "kilometers") {
		t.Fatal("metric systemPrompt missing km line")
	}
}
