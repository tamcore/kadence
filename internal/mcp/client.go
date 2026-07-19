package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Recognized Server.Transport values.
const (
	transportStreamableHTTP = "streamable-http"
	transportSSE            = "sse"
)

// ToolInfo describes one tool discovered on a remote MCP server.
type ToolInfo struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// mcpClient is the registry's seam onto a single remote MCP server
// connection, satisfied by the real mark3labs/mcp-go client below (and by
// fakes in tests, if needed).
type mcpClient interface {
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name, argsJSON string) (string, error)
	Close() error
}

// realMCPClient wraps a mark3labs/mcp-go client over a network transport
// (streamable-http or sse), with an initialized MCP session.
type realMCPClient struct {
	client *mcpclient.Client
}

// newClient builds and initializes a real MCP client for the given server
// definition. It picks the transport (streamable-http or sse), applies
// HTTP Basic auth via a header option when credentials are configured, and
// performs the MCP initialize handshake.
func newClient(ctx context.Context, s Server) (mcpClient, error) {
	c, err := newTransportClient(s)
	if err != nil {
		return nil, err
	}

	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("mcp: start client for %s/%s: %w", s.Name, s.Scope, err)
	}

	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "kadence", Version: "0.0.1"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialize %s/%s: %w", s.Name, s.Scope, err)
	}

	return &realMCPClient{client: c}, nil
}

// newTransportClient constructs the mcp-go client for the server's
// configured transport, without starting or initializing it.
func newTransportClient(s Server) (*mcpclient.Client, error) {
	headers := basicAuthHeaders(s)

	switch s.Transport {
	case transportStreamableHTTP:
		opts := []transport.StreamableHTTPCOption{}
		if len(headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(headers))
		}
		c, err := mcpclient.NewStreamableHttpClient(s.URL, opts...)
		if err != nil {
			return nil, fmt.Errorf("mcp: new streamable-http client for %s/%s: %w", s.Name, s.Scope, err)
		}
		return c, nil
	case transportSSE:
		opts := []transport.ClientOption{}
		if len(headers) > 0 {
			opts = append(opts, transport.WithHeaders(headers))
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

// basicAuthHeaders returns the HTTP headers to apply for the server's
// configured basic-auth credentials, or nil if no AuthUser is set.
func basicAuthHeaders(s Server) map[string]string {
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
		return nil, fmt.Errorf("mcp: list tools: %w", err)
	}

	infos := make([]ToolInfo, 0, len(result.Tools))
	for _, t := range result.Tools {
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp: marshal schema for tool %s: %w", t.Name, err)
		}
		infos = append(infos, ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Schema:      schema,
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
		return "", fmt.Errorf("mcp: call tool %s: %w", name, err)
	}

	text := flattenTextContent(result.Content)
	if result.IsError {
		return "", fmt.Errorf("mcp: tool %s returned an error: %s", name, text)
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
