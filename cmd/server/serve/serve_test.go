package serve

import (
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
