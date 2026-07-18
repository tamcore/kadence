// Package provider abstracts LLM chat completion behind a pluggable interface.
// Phase 3a ships a single OpenAI-compatible implementation.
package provider

import "context"

// Message is one chat message in a provider request.
type Message struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}

// ChatRequest is a streaming chat-completion request.
type ChatRequest struct {
	Messages    []Message
	Model       string
	MaxTokens   int
	Temperature float64
}

// TokenFunc receives streamed content deltas.
type TokenFunc func(delta string) error

// Provider streams an assistant reply for a chat request.
type Provider interface {
	// StreamChat streams the reply, invoking onToken for each content delta,
	// and returns the full assembled assistant text.
	StreamChat(ctx context.Context, req ChatRequest, onToken TokenFunc) (string, error)
}
