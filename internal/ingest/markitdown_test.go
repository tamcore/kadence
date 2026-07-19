package ingest_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/tamcore/kadence/internal/ingest"
)

// canned markitdown-style output for the fake convert_to_markdown tool.
const cannedMarkdown = "# Marathon Plan\n\nWeek 1..."

// newFakeMarkitdownServer stands up a real mcp-go MCP server (streamable-http
// transport) over httptest, registering a convert_to_markdown tool that
// records the received uri and returns canned markdown as text content.
// Modeled on internal/mcp/registry_test.go's newFakeGarminServer.
func newFakeMarkitdownServer(t *testing.T) (ts *httptest.Server, receivedURI *string) {
	t.Helper()

	var mu sync.Mutex
	var uri string

	srv := mcpserver.NewMCPServer("fake-markitdown", "0.0.1")
	tool := mcpgo.NewTool("convert_to_markdown",
		mcpgo.WithDescription("Convert a resource (data/http/https/file URI) to markdown."),
		mcpgo.WithString("uri", mcpgo.Description("URI of the resource to convert"), mcpgo.Required()),
	)
	srv.AddTool(tool, func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		mu.Lock()
		uri = req.GetString("uri", "")
		mu.Unlock()
		return mcpgo.NewToolResultText(cannedMarkdown), nil
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(srv)
	testServer := httptest.NewServer(httpSrv)
	t.Cleanup(testServer.Close)

	return testServer, &uri
}

func TestMarkitdownExtractor_CanHandle(t *testing.T) {
	ts, _ := newFakeMarkitdownServer(t)

	ex, err := ingest.NewMarkitdownExtractor(ts.URL, "", "", "streamable-http")
	if err != nil {
		t.Fatalf("NewMarkitdownExtractor: %v", err)
	}

	if !ex.CanHandle("application/pdf") {
		t.Fatal("CanHandle(application/pdf) = false, want true")
	}
	if !ex.CanHandle("image/png") {
		t.Fatal("CanHandle(image/png) = false, want true")
	}
	if !ex.CanHandle("image/jpeg") {
		t.Fatal("CanHandle(image/jpeg) = false, want true")
	}
}

func TestMarkitdownExtractor_ExtractPDF(t *testing.T) {
	ts, receivedURI := newFakeMarkitdownServer(t)

	ex, err := ingest.NewMarkitdownExtractor(ts.URL, "", "", "streamable-http")
	if err != nil {
		t.Fatalf("NewMarkitdownExtractor: %v", err)
	}

	data := []byte("%PDF-1.4 fake pdf bytes")
	res, err := ex.Extract(context.Background(), data, "application/pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if !strings.Contains(res.Markdown, "Marathon Plan") {
		t.Fatalf("Markdown missing canned text: %q", res.Markdown)
	}
	if res.SourceType != "pdf" {
		t.Fatalf("SourceType = %q, want %q", res.SourceType, "pdf")
	}

	wantPrefix := "data:application/pdf;base64,"
	if !strings.HasPrefix(*receivedURI, wantPrefix) {
		t.Fatalf("server received uri %q, want prefix %q", *receivedURI, wantPrefix)
	}
	b64 := strings.TrimPrefix(*receivedURI, wantPrefix)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode base64 uri payload: %v", err)
	}
	if string(decoded) != string(data) {
		t.Fatalf("decoded uri payload = %q, want %q", decoded, data)
	}
}
