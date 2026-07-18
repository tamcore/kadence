package ingest

import (
	"strings"
	"testing"
)

func TestChunkTextEmpty(t *testing.T) {
	if got := ChunkText("", 100); len(got) != 0 {
		t.Fatalf("empty → no chunks, got %v", got)
	}
}

func TestChunkTextNonPositiveMaxChars(t *testing.T) {
	if got := ChunkText("some text here", 0); got != nil {
		t.Fatalf("maxChars=0 → nil, got %v", got)
	}
	if got := ChunkText("some text here", -5); got != nil {
		t.Fatalf("maxChars<0 → nil, got %v", got)
	}
}

func TestChunkTextSingleSmall(t *testing.T) {
	got := ChunkText("hello world", 100)
	if len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("small → 1 chunk: %v", got)
	}
}

func TestChunkTextSplitsOnParagraphs(t *testing.T) {
	text := "para one is here.\n\npara two is here.\n\npara three is here."
	got := ChunkText(text, 20)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks, got %d: %v", len(got), got)
	}
	for _, c := range got {
		if len(c) == 0 {
			t.Fatalf("no empty chunks allowed: %v", got)
		}
	}
}

func TestChunkTextHardSplitsGiantParagraph(t *testing.T) {
	var giant strings.Builder
	for range 50 {
		giant.WriteString("word ")
	}
	got := ChunkText(giant.String(), 40)
	if len(got) < 2 {
		t.Fatalf("giant paragraph must hard-split: %d chunks", len(got))
	}
	for _, c := range got {
		if len(c) > 40 {
			t.Fatalf("chunk exceeds maxChars: %q", c)
		}
	}
}
