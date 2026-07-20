package knowledge_test

import (
	"testing"

	"github.com/tamcore/kadence/internal/knowledge"
)

func TestTopTermsRarerTermOutranksUbiquitousAtEqualCount(t *testing.T) {
	// "widget" appears once in each of 3 chunks (ubiquitous, so its raw count
	// across the corpus is 3); "gizmo" appears 3 times but confined to a
	// single chunk (rare). Equal raw total count, but gizmo's IDF is much
	// higher (df=1 vs df=3), so its summed weight should rank above widget's.
	chunks := []string{
		"widget alpha",
		"widget beta",
		"widget gizmo gizmo gizmo",
	}

	terms := knowledge.TopTerms(chunks, 10)

	var gizmoRank, widgetRank = -1, -1
	for i, term := range terms {
		switch term.Term {
		case "gizmo":
			gizmoRank = i
		case "widget":
			widgetRank = i
		}
	}
	if gizmoRank == -1 || widgetRank == -1 {
		t.Fatalf("expected both gizmo and widget present: %+v", terms)
	}
	if gizmoRank >= widgetRank {
		t.Fatalf("expected rarer term 'gizmo' (rank %d) to outrank ubiquitous 'widget' (rank %d)", gizmoRank, widgetRank)
	}
}

func TestTopTermsRemovesShortTokensAndStopwords(t *testing.T) {
	chunks := []string{"the cat and a dog with your for this that but not have has been from"}

	terms := knowledge.TopTerms(chunks, 20)

	for _, term := range terms {
		if len(term.Term) < 3 {
			t.Fatalf("expected tokens shorter than 3 chars to be removed, got %q", term.Term)
		}
		switch term.Term {
		case "the", "and", "for", "you", "your", "with", "that", "this", "not", "but", "have", "has", "from", "been":
			t.Fatalf("expected stopword %q to be removed", term.Term)
		}
	}
}

func TestTopTermsDeterministicTieBreak(t *testing.T) {
	chunks := []string{"zeta alpha beta gamma delta"}

	first := knowledge.TopTerms(chunks, 10)
	second := knowledge.TopTerms(chunks, 10)

	if len(first) != len(second) {
		t.Fatalf("expected deterministic length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("expected deterministic order at index %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	// All terms tie at weight/count (each appears once, one chunk), so the
	// tie-break must be alphabetical ascending.
	want := []string{"alpha", "beta", "delta", "gamma", "zeta"}
	for i, term := range first {
		if term.Term != want[i] {
			t.Fatalf("expected alphabetical tie-break order %v, got %+v", want, first)
		}
	}
}

func TestTopTermsEmptyInputReturnsNil(t *testing.T) {
	if got := knowledge.TopTerms(nil, 10); got != nil {
		t.Fatalf("expected nil for empty chunks, got %+v", got)
	}
	if got := knowledge.TopTerms([]string{"some content here"}, 0); got != nil {
		t.Fatalf("expected nil for n<=0, got %+v", got)
	}
}
