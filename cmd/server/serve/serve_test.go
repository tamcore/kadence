package serve

import (
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/ingest"
)

// buildIngestExtractors is pure enough to unit test without refactoring
// serve.go: it only depends on config.Config (a value, no DB/network
// startup) and internal/ingest + internal/mcp constructors that fail fast on
// bad input rather than dialing out. Extracting the rest of Run() into
// testable functions (DB pool, chat service wiring, HTTP server lifecycle)
// would require a larger refactor and is out of scope for this test-gap pass.

func TestBuildIngestExtractorsMarkitdownDisabled(t *testing.T) {
	cfg := config.Config{} // MarkitdownURL == "" => MarkitdownEnabled() == false

	extractors := buildIngestExtractors(cfg)

	if len(extractors) != 1 {
		t.Fatalf("len(extractors) = %d, want 1 (PDF-only fallback)", len(extractors))
	}
	if _, ok := extractors[0].(*ingest.PDFExtractor); !ok {
		t.Fatalf("extractors[0] = %T, want *ingest.PDFExtractor", extractors[0])
	}
	capabilities := ingest.BuildUploadCapabilities(extractors, 10<<20)
	if capabilities.RichExtraction || capabilities.Accept != "application/pdf,.pdf" {
		t.Fatalf("capabilities = %+v, want effective PDF-only profile", capabilities)
	}
}

func TestBuildIngestExtractorsMarkitdownEnabled(t *testing.T) {
	cfg := config.Config{
		MarkitdownURL:       "https://markitdown.example.test/mcp",
		MarkitdownTransport: "streamable-http",
	}

	extractors := buildIngestExtractors(cfg)

	if len(extractors) != 2 {
		t.Fatalf("len(extractors) = %d, want 2 (markitdown + PDF fallback)", len(extractors))
	}
	if _, ok := extractors[0].(*ingest.MarkitdownExtractor); !ok {
		t.Fatalf("extractors[0] = %T, want *ingest.MarkitdownExtractor", extractors[0])
	}
	if _, ok := extractors[1].(*ingest.PDFExtractor); !ok {
		t.Fatalf("extractors[1] = %T, want *ingest.PDFExtractor", extractors[1])
	}
	capabilities := ingest.BuildUploadCapabilities(extractors, 10<<20)
	if !capabilities.RichExtraction {
		t.Fatalf("capabilities = %+v, want rich extraction from effective extractor set", capabilities)
	}
	if !strings.Contains(capabilities.Accept, ".docx") || !strings.Contains(capabilities.Accept, ".png") {
		t.Fatalf("capabilities.Accept = %q, want rich document and image formats", capabilities.Accept)
	}
}

func TestBuildIngestExtractorsFallsBackWhenCAFileUnreadable(t *testing.T) {
	cfg := config.Config{
		MarkitdownURL: "https://markitdown.example.test/mcp",
		MCPCAFile:     "/nonexistent/path/to/ca.pem",
	}

	extractors := buildIngestExtractors(cfg)

	if len(extractors) != 1 {
		t.Fatalf("len(extractors) = %d, want 1 (PDF-only fallback on CA read failure)", len(extractors))
	}
	if _, ok := extractors[0].(*ingest.PDFExtractor); !ok {
		t.Fatalf("extractors[0] = %T, want *ingest.PDFExtractor", extractors[0])
	}
}

func TestBuildIngestExtractorsFallsBackWhenRichExtractorConfigInvalid(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		transport string
	}{
		{name: "transport", url: "https://extractor.example.test/mcp", transport: "stdio"},
		{name: "URL", url: "ftp://extractor.example.test/mcp", transport: "streamable-http"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractors := buildIngestExtractors(config.Config{
				MarkitdownURL:       tt.url,
				MarkitdownTransport: tt.transport,
			})

			capabilities := ingest.BuildUploadCapabilities(extractors, 10<<20)
			if len(extractors) != 1 || capabilities.RichExtraction {
				t.Fatalf(
					"extractors=%d capabilities=%+v, want effective PDF-only fallback",
					len(extractors),
					capabilities,
				)
			}
			if capabilities.Accept != "application/pdf,.pdf" {
				t.Fatalf("Accept=%q, want PDF-only profile", capabilities.Accept)
			}
		})
	}
}
