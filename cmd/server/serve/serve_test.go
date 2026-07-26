package serve

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

func TestScheduledChatWiringUsesSharedServiceOnlyWhenEnabled(t *testing.T) {
	service := &scheduled.Service{}
	tasks := &store.ScheduledTaskRepository{}

	disabled := newScheduledChatWiring(false, service, tasks)
	if disabled.handoff != nil || disabled.hydrator != nil || disabled.pauser != nil {
		t.Fatalf("disabled wiring = %+v, want nil dependencies", disabled)
	}
	enabled := newScheduledChatWiring(true, service, tasks)
	if enabled.handoff != service || enabled.hydrator != service || enabled.pauser != tasks {
		t.Fatalf("enabled wiring does not share service/repository: %+v", enabled)
	}
}

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

func TestBuildChatContentSharesEffectiveRichExtractorsWithAttachmentProcessor(t *testing.T) {
	content := buildChatContent(config.Config{
		MarkitdownURL:       "https://markitdown.example.test/mcp",
		MarkitdownTransport: "streamable-http",
	})
	if len(content.extractors) != 2 {
		t.Fatalf("extractors = %d, want rich + PDF fallback", len(content.extractors))
	}

	prepared, err := content.attachments.Prepare([]chat.FileInput{{
		Filename: "training.docx",
		MIME:     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Data:     []byte("raw office bytes"),
	}})
	if err != nil {
		t.Fatalf("Prepare rich chat document: %v", err)
	}
	if len(prepared) != 1 || prepared[0].Kind != model.AttachmentKindDocument {
		t.Fatalf("prepared rich chat document = %+v", prepared)
	}
}

func TestBuildChatContentUsesEffectivePDFOnlyFallback(t *testing.T) {
	content := buildChatContent(config.Config{})
	if len(content.extractors) != 1 {
		t.Fatalf("extractors = %d, want PDF-only", len(content.extractors))
	}

	_, err := content.attachments.Prepare([]chat.FileInput{{
		Filename: "training.docx",
		MIME:     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Data:     []byte("raw office bytes"),
	}})
	if !errors.Is(err, chat.ErrUnsupportedAttachment) {
		t.Fatalf("Prepare rich chat document error = %v, want ErrUnsupportedAttachment", err)
	}
}

type uploadLimitChatStreamer struct {
	calls int
}

func (s *uploadLimitChatStreamer) Stream(
	context.Context,
	int64,
	chat.UserContext,
	string,
	string,
	chat.EventSink,
) error {
	s.calls++
	return nil
}

func (s *uploadLimitChatStreamer) StreamTurn(
	context.Context,
	int64,
	chat.UserContext,
	string,
	chat.TurnInput,
	chat.EventSink,
) error {
	s.calls++
	return nil
}

func TestNewChatHandlerUsesConfiguredAggregateUploadLimit(t *testing.T) {
	streamer := &uploadLimitChatStreamer{}
	handler := newChatHandler(
		config.Config{UploadMaxBytes: 8},
		streamer,
		nil,
		nil,
		nil,
		nil,
	)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("files", "too-large.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("123456789")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/chat", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request = request.WithContext(auth.ContextWithUser(
		request.Context(),
		&model.User{ID: 7, Username: "u", Role: model.RoleUser},
	))
	response := httptest.NewRecorder()

	handler.Send(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want=413 body=%s", response.Code, response.Body.String())
	}
	if streamer.calls != 0 {
		t.Fatalf("chat service called %d times before size validation", streamer.calls)
	}
}

var _ handlers.ChatStreamer = (*uploadLimitChatStreamer)(nil)
var _ handlers.ChatTurnStreamer = (*uploadLimitChatStreamer)(nil)
