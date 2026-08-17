package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Recognized Server.Transport values.
const (
	transportStreamableHTTP = "streamable-http"
	transportSSE            = "sse"
)

const (
	// maxToolResultBytes caps one tool result. Remote MCP servers are not
	// trusted to bound their own output, and the result flows straight into the
	// chat request, so an unbounded response would inflate memory and tokens.
	// Well above realistic tool output.
	maxToolResultBytes = 256 << 10

	// toolResultTruncatedMarker tells the model the result was cut, so it does
	// not silently reason over a partial payload.
	toolResultTruncatedMarker = "\n[truncated: response exceeded 256KiB]"

	// maxToolSchemaBytes caps one tool's JSON input schema. Schemas are remote
	// input that becomes part of every chat request's tool definitions, so an
	// unbounded schema inflates every turn, not just one result.
	maxToolSchemaBytes = 32 << 10

	// maxToolDescriptionBytes caps one tool's description, same reasoning.
	maxToolDescriptionBytes = 4 << 10

	// placeholderToolSchema replaces an oversized schema. A schema must stay
	// valid JSON, so it cannot be truncated like free text; an unconstrained
	// object still lets the model call the tool, and the reason is in the log.
	placeholderToolSchema = `{"type":"object"}`
)

// truncateToolResult caps s at maxToolResultBytes, cutting on a rune boundary
// and appending toolResultTruncatedMarker.
func truncateToolResult(s string) string {
	return truncateTo(s, maxToolResultBytes)
}

// truncateTo caps s at limit bytes on a rune boundary, appending
// toolResultTruncatedMarker.
func truncateTo(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + toolResultTruncatedMarker
}

// boundedError renders a capped message while keeping the original error's
// chain reachable, so errors.Is/As still work on a bounded error.
type boundedError struct {
	msg string
	err error
}

func (e *boundedError) Error() string { return e.msg }
func (e *boundedError) Unwrap() error { return e.err }

// boundError caps err's rendered message at maxToolResultBytes. A transport
// failure carries the remote server's response body, and internal/chat turns
// that text into a tool result the model reads — so it needs the same cap as a
// successful result. Errors already within the cap are returned unchanged.
func boundError(err error) error {
	if err == nil || len(err.Error()) <= maxToolResultBytes {
		return err
	}
	return &boundedError{msg: truncateTo(err.Error(), maxToolResultBytes), err: err}
}

// capToolSchema replaces an oversized tool schema with placeholderToolSchema.
func capToolSchema(name string, schema json.RawMessage) json.RawMessage {
	if len(schema) <= maxToolSchemaBytes {
		return schema
	}
	slog.Warn("mcp tool schema exceeds the cap; advertising an unconstrained schema instead",
		"tool", name, "schema_bytes", len(schema), "cap_bytes", maxToolSchemaBytes)
	return json.RawMessage(placeholderToolSchema)
}

// ToolInfo describes one tool discovered on a remote MCP server.
type ToolInfo struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ErrToolRefused marks a result the remote server itself produced: the call
// reached it, it decided against running, and it said so. The connection is
// healthy, so a caller must not treat this like a transport fault.
var ErrToolRefused = errors.New("mcp: the server refused the call")

// mcpClient is the registry's seam onto a single remote MCP server
// connection, satisfied by the real mark3labs/mcp-go client below (and by
// fakes in tests, if needed).
type mcpClient interface {
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name, argsJSON string) (string, error)
	Close() error
}

// Client is the exported subset of mcpClient (tool invocation + shutdown,
// no discovery) reusable by callers outside this package that talk to a
// single, known remote MCP server — e.g. the ingest package's markitdown
// extractor.
type Client interface {
	CallTool(ctx context.Context, name, argsJSON string) (string, error)
	Close() error
}

// NewClient builds and initializes an exported MCP client for a single
// remote server, reusing the same streamable-http/sse transport and
// basic-auth plumbing as the internal registry client. httpClient, if
// non-nil (e.g. from HTTPClientWithCA), is used for the transport instead of
// mcp-go's default client — used to verify the server's TLS cert against a
// custom CA. Pass nil to preserve today's behavior.
func NewClient(ctx context.Context, url, transport, authUser, authPass string, httpClient *http.Client) (Client, error) {
	return newClient(ctx, Server{
		URL:       url,
		Transport: transport,
		AuthUser:  authUser,
		AuthPass:  authPass,
	}, httpClient)
}

// realMCPClient wraps a mark3labs/mcp-go client over a network transport
// (streamable-http or sse), with an initialized MCP session.
type realMCPClient struct {
	client *mcpclient.Client
}

// newClient builds and initializes a real MCP client for the given server
// definition. It picks the transport (streamable-http or sse), applies
// HTTP Basic auth via a header option when credentials are configured, and
// performs the MCP initialize handshake. httpClient, if non-nil, is used for
// the underlying transport instead of mcp-go's default client (see
// newTransportClient).
func newClient(ctx context.Context, s Server, httpClient *http.Client) (mcpClient, error) {
	c, err := newTransportClient(s, httpClient)
	if err != nil {
		return nil, err
	}

	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: start client for %s/%s: %w", s.Name, s.Scope, err)
	}

	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "kadence", Version: "0.0.1"}
	if wantsElicitation(s) {
		// Declared unconditionally for a per-principal server: without the
		// capability the server refuses every tool that needs confirmation,
		// and whether a given call can actually reach a user is decided per
		// call, not per connection.
		initReq.Params.Capabilities.Elicitation = &mcpgo.ElicitationCapability{}
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialize %s/%s: %w", s.Name, s.Scope, err)
	}

	return &realMCPClient{client: c}, nil
}

// newTransportClient constructs the mcp-go client for the server's
// configured transport, without starting or initializing it. httpClient, if
// non-nil, is applied as the transport's underlying HTTP client (used to
// verify HTTPS MCP servers' certs against a custom CA); nil leaves mcp-go's
// default client in place, preserving today's behavior.
func newTransportClient(s Server, httpClient *http.Client) (*mcpclient.Client, error) {
	headers := authHeaders(s)

	switch s.Transport {
	case transportStreamableHTTP:
		opts := []transport.StreamableHTTPCOption{}
		if len(headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(headers))
		}
		if httpClient != nil {
			opts = append(opts, transport.WithHTTPBasicClient(httpClient))
		}
		// Built in two steps rather than through NewStreamableHttpClient: the
		// elicitation handler is a client option, and that constructor takes
		// only transport options.
		tr, err := transport.NewStreamableHTTP(s.URL, opts...)
		if err != nil {
			return nil, fmt.Errorf("mcp: new streamable-http client for %s/%s: %w", s.Name, s.Scope, err)
		}
		return mcpclient.NewClient(tr, clientOptions(s)...), nil
	case transportSSE:
		opts := []transport.ClientOption{}
		if len(headers) > 0 {
			opts = append(opts, transport.WithHeaders(headers))
		}
		if httpClient != nil {
			opts = append(opts, transport.WithHTTPClient(httpClient))
		}
		c, err := mcpclient.NewSSEMCPClient(s.URL, opts...)
		if err != nil {
			return nil, fmt.Errorf("mcp: new sse client for %s/%s: %w", s.Name, s.Scope, err)
		}
		return c, nil
	default:
		return nil, fmt.Errorf("mcp: unknown transport %q for server %s/%s", s.Transport, s.Name, s.Scope)
	}
}

// wantsElicitation reports whether this server may ask its caller to confirm.
//
// Two conditions. Only a per-principal server can be asked at all: a
// confirmation names a user, and a server on one shared credential has none to
// name. And only the streamable-http transport can carry the question —
// mcp-go's SSE transport has no path for a server-initiated request, so
// declaring the capability over it would promise something we cannot answer,
// leaving the server waiting on a reply that can never arrive.
func wantsElicitation(s Server) bool {
	return s.PerPrincipal() && s.Transport == transportStreamableHTTP
}

// clientOptions returns the mcp-go client options for this server. The
// elicitation handler is stateless — it reads the user and the tool from the
// context of the call being confirmed — so one instance serves every caller
// of a cached client.
func clientOptions(s Server) []mcpclient.ClientOption {
	if !wantsElicitation(s) {
		return nil
	}
	return []mcpclient.ClientOption{mcpclient.WithElicitationHandler(elicitHandler{})}
}

// authHeaders returns the Authorization header for this server: the user's own
// bearer token when the credential is per user, the shared basic-auth
// credential otherwise, and nil when neither is configured.
func authHeaders(s Server) map[string]string {
	if s.PerPrincipal() {
		if s.bearer == "" {
			return nil
		}
		return map[string]string{"Authorization": "Bearer " + s.bearer}
	}
	if s.AuthUser == "" {
		return nil
	}
	token := base64.StdEncoding.EncodeToString([]byte(s.AuthUser + ":" + s.AuthPass))
	return map[string]string{"Authorization": "Basic " + token}
}

// ListTools lists all tools available on the remote server.
func (c *realMCPClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	result, err := c.client.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		return nil, boundError(fmt.Errorf("mcp: list tools: %w", err))
	}

	infos := make([]ToolInfo, 0, len(result.Tools))
	for _, t := range result.Tools {
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp: marshal schema for tool %s: %w", t.Name, err)
		}
		infos = append(infos, ToolInfo{
			Name:        t.Name,
			Description: truncateTo(t.Description, maxToolDescriptionBytes),
			Schema:      capToolSchema(t.Name, schema),
		})
	}
	return infos, nil
}

// CallTool invokes the named tool with the given JSON-encoded arguments and
// flattens the result's text content blocks into a single string. An MCP
// error result is returned as an error.
func (c *realMCPClient) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("mcp: unmarshal arguments for tool %s: %w", name, err)
		}
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.client.CallTool(ctx, req)
	if err != nil {
		// Bounded: a transport failure carries the remote body, and the caller
		// forwards this text to the model as the tool result.
		return "", boundError(fmt.Errorf("mcp: call tool %s: %w", name, err))
	}

	text := truncateToolResult(flattenTextContent(result.Content))
	if result.IsError {
		return "", fmt.Errorf("%w: tool %s: %s", ErrToolRefused, name, text)
	}
	return text, nil
}

// flattenTextContent concatenates all TextContent blocks in an MCP tool
// result into a single string.
func flattenTextContent(content []mcpgo.Content) string {
	var b strings.Builder
	for _, item := range content {
		if tc, ok := item.(mcpgo.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Close shuts down the underlying transport connection.
func (c *realMCPClient) Close() error {
	return c.client.Close()
}
