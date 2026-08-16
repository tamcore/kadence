package skill

import "testing"

func TestParseRejectsMissingFrontmatter(t *testing.T) {
	if _, err := parse([]byte("no frontmatter here")); err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}
