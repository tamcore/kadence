package ingest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tamcore/kadence/internal/mcp"
	"github.com/tamcore/kadence/internal/model"
)

// convertToolName is the single tool markitdown-mcp exposes, per its README:
// convert_to_markdown(uri) accepting data:/http:/https:/file: URIs.
const convertToolName = "convert_to_markdown"

// markitdownMimePrefixes lists the broad, inclusive set of MIME type
// prefixes/exact matches this extractor will attempt to convert via
// markitdown-mcp: documents, images, and office formats it's known to
// support, plus generic text.
var markitdownMimePrefixes = []string{
	"application/pdf",
	"image/",
	"text/",
	"application/msword",
	"application/vnd.openxmlformats-officedocument",
	"application/vnd.ms-excel",
	"application/vnd.ms-powerpoint",
	"application/rtf",
	"application/epub+zip",
	"text/html",
}

// MarkitdownExtractor converts uploaded documents (PDFs, images, office
// formats, ...) to markdown by delegating to a remote markitdown-mcp server
// over MCP, via its single convert_to_markdown(uri) tool.
type MarkitdownExtractor struct {
	url        string
	transport  string
	authUser   string
	authPass   string
	httpClient *http.Client // optional CA-verifying client; nil = mcp-go default
}

// NewMarkitdownExtractor builds a MarkitdownExtractor targeting the
// markitdown-mcp server at url over the given transport ("streamable-http"
// or "sse"), with optional HTTP Basic auth credentials. It does not connect;
// each Extract dials a fresh client, so app startup does not depend on the
// markitdown server being ready and a server restart cannot leave the
// extractor wedged to a dead connection. httpClient, if non-nil (e.g. from
// mcp.HTTPClientWithCA), is used for the MCP transport instead of mcp-go's
// default client — used to verify the markitdown server's TLS cert against
// a custom CA. Pass nil to preserve today's behavior.
func NewMarkitdownExtractor(url, authUser, authPass, transport string, httpClient *http.Client) (*MarkitdownExtractor, error) {
	if url == "" {
		return nil, fmt.Errorf("markitdown: url is required")
	}
	return &MarkitdownExtractor{
		url: url, transport: transport, authUser: authUser, authPass: authPass, httpClient: httpClient,
	}, nil
}

// CanHandle reports whether mime is one markitdown-mcp is expected to
// convert: PDFs, images, office documents, and text/html.
func (e *MarkitdownExtractor) CanHandle(mime string) bool {
	for _, prefix := range markitdownMimePrefixes {
		if strings.HasPrefix(mime, prefix) {
			return true
		}
	}
	return false
}

// Extract base64-encodes data into a data: URI (tagged with mime) and asks
// markitdown-mcp to convert it to markdown via convert_to_markdown.
func (e *MarkitdownExtractor) Extract(ctx context.Context, data []byte, mime string) (Result, error) {
	dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)

	argsJSON, err := json.Marshal(map[string]string{"uri": dataURI})
	if err != nil {
		return Result{}, fmt.Errorf("markitdown: marshal arguments: %w", err)
	}

	client, err := mcp.NewClient(ctx, e.url, e.transport, e.authUser, e.authPass, e.httpClient)
	if err != nil {
		return Result{}, fmt.Errorf("markitdown: connect to %s: %w", e.url, err)
	}
	defer func() { _ = client.Close() }()

	markdown, err := client.CallTool(ctx, convertToolName, string(argsJSON))
	if err != nil {
		return Result{}, fmt.Errorf("markitdown: convert_to_markdown: %w", err)
	}

	return Result{
		Markdown:   markdown,
		SourceType: sourceTypeForMime(mime),
	}, nil
}

// sourceTypeForMime maps a MIME type to one of the model.DocSource*
// constants: application/pdf -> pdf, image/* -> image, else -> text.
func sourceTypeForMime(mime string) string {
	switch {
	case mime == pdfMimeType:
		return model.DocSourcePDF
	case strings.HasPrefix(mime, "image/"):
		return model.DocSourceImage
	default:
		return model.DocSourceText
	}
}
