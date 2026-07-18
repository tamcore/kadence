package ingest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSelectPicksByMime(t *testing.T) {
	pdf := NewPDFExtractor()
	got, err := Select([]Extractor{pdf}, "application/pdf")
	if err != nil || got == nil {
		t.Fatalf("expected pdf extractor: %v", err)
	}
	if _, err := Select([]Extractor{pdf}, "image/png"); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("expected ErrUnsupportedType, got %v", err)
	}
}

func TestPDFExtractorExtractsText(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Skipf("no sample.pdf fixture: %v", err)
	}
	res, err := NewPDFExtractor().Extract(context.Background(), data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.SourceType != "pdf" || len(res.Markdown) == 0 {
		t.Fatalf("empty extraction: %+v", res)
	}
	if !strings.Contains(res.Markdown, "Kadence") {
		t.Fatalf("expected fixture text in output: %q", res.Markdown)
	}
}
