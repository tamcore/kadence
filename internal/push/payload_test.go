package push

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildSnippetTruncatesRuneSafe(t *testing.T) {
	if got := BuildSnippet("hello", 120); got != "hello" {
		t.Fatalf("short unchanged, got %q", got)
	}
	long := strings.Repeat("é", 200) // multi-byte runes
	got := BuildSnippet(long, 120)
	if utf8.RuneCountInString(got) > 121 { // 120 + ellipsis
		t.Fatalf("too long: %d runes", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
}
