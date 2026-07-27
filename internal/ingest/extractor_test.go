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
	got, err := Select([]Extractor{pdf}, pdfMimeType)
	if err != nil || got == nil {
		t.Fatalf("expected pdf extractor: %v", err)
	}
	if _, err := Select([]Extractor{pdf}, "image/png"); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("expected ErrUnsupportedType, got %v", err)
	}
}

func TestBuildUploadCapabilitiesPDFOnly(t *testing.T) {
	got := BuildUploadCapabilities([]Extractor{NewPDFExtractor()}, 8<<20)

	if got.MaxBytes != 8<<20 {
		t.Fatalf("MaxBytes = %d, want %d", got.MaxBytes, 8<<20)
	}
	if got.RichExtraction {
		t.Fatal("RichExtraction = true, want false for the effective PDF-only extractor set")
	}
	if got.Accept != "application/pdf,.pdf" {
		t.Fatalf("Accept = %q, want PDF-only browser accept string", got.Accept)
	}
}

func TestBuildUploadCapabilitiesIncludesRichFormatsFromEffectiveExtractors(t *testing.T) {
	rich, err := NewMarkitdownExtractor("https://extractor.example.test/mcp", "", "", "streamable-http", nil)
	if err != nil {
		t.Fatalf("NewMarkitdownExtractor: %v", err)
	}

	got := BuildUploadCapabilities([]Extractor{rich, NewPDFExtractor()}, 16<<20)

	if !got.RichExtraction {
		t.Fatal("RichExtraction = false, want true when the effective extractor set handles rich formats")
	}
	for _, want := range []string{
		pdfMimeType, ".pdf",
		"image/png", ".png", "image/jpeg", ".jpg", ".jpeg", "image/webp", ".webp", "image/gif", ".gif",
		"text/plain", ".txt", "text/markdown", ".md", mimeTextHTML, ".html", "text/csv", ".csv",
		docMimeWord, ".doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx",
		docMimeExcel, ".xls",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx",
		docMimePowerPoint, ".ppt",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx",
		docMimeRTF, ".rtf", docMimeEPUB, ".epub",
	} {
		if !strings.Contains(","+got.Accept+",", ","+want+",") {
			t.Errorf("Accept %q does not contain exact entry %q", got.Accept, want)
		}
	}
}

func TestPDFExtractorExtractsText(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Skipf("no sample.pdf fixture: %v", err)
	}
	res, err := NewPDFExtractor().Extract(context.Background(), data, pdfMimeType)
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
