// Package provider abstracts LLM chat completion behind a pluggable interface.
// Phase 3a ships a single OpenAI-compatible implementation.
package provider

import (
	"context"
	"encoding/json"
)

// ToolDefinition describes a tool the model may call (JSON-schema parameters).
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is a tool invocation the model requested.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON object
}

// StreamResult is the outcome of one streamed completion: assembled content
// plus any tool calls the model requested.
type StreamResult struct {
	Content   string
	ToolCalls []ToolCall
}

// Message is one chat message in a provider request.
type Message struct {
	Role       string // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []ToolCall `json:",omitempty"` // assistant → tool-call request
	ToolCallID string     `json:",omitempty"` // role="tool" → which call this answers
	Name       string     `json:",omitempty"` // role="tool" → tool name
}

// ChatRequest is a streaming chat-completion request.
type ChatRequest struct {
	Messages    []Message
	Model       string
	MaxTokens   int
	Temperature float64
	Tools       []ToolDefinition
}

// TokenFunc receives streamed content deltas.
type TokenFunc func(delta string) error

// Provider streams an assistant reply for a chat request.
type Provider interface {
	// StreamChat streams the reply, invoking onToken for each content delta,
	// and returns the full assembled assistant text.
	StreamChat(ctx context.Context, req ChatRequest, onToken TokenFunc) (string, error)

	// StreamChatWithTools streams the reply like StreamChat, but also
	// supports tool-calling: it returns any tool calls the model requested
	// alongside the assembled content.
	StreamChatWithTools(ctx context.Context, req ChatRequest, onToken TokenFunc) (StreamResult, error)
}
