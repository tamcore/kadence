// Package chat orchestrates streaming LLM conversations.
package chat

// Event types emitted over SSE.
const (
	EventMeta  = "meta"
	EventToken = "token"
	EventDone  = "done"
	EventError = "error"
)

// ChatEvent is a single server-sent event in a chat stream.
type ChatEvent struct {
	Type           string `json:"type"`
	Delta          string `json:"delta,omitempty"`
	ConversationID int64  `json:"conversationId,omitempty"`
	Message        string `json:"message,omitempty"`
}

// EventSink receives chat events (implemented by the SSE handler).
type EventSink interface {
	Send(ChatEvent) error
	Flush() error
}
