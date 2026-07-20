// Package chat orchestrates streaming LLM conversations.
package chat

// Event types emitted over SSE.
const (
	EventMeta        = "meta"
	EventToken       = "token"
	EventDone        = "done"
	EventError       = "error"
	EventTool        = "tool"
	EventCredentials = "credentials_request"
)

// ChatEvent is a single server-sent event in a chat stream.
type ChatEvent struct {
	Type           string            `json:"type"`
	Delta          string            `json:"delta,omitempty"`
	ConversationID string            `json:"conversationId,omitempty"`
	Message        string            `json:"message,omitempty"`
	Tool           string            `json:"tool,omitempty"`
	Status         string            `json:"status,omitempty"`
	Arguments      string            `json:"arguments,omitempty"`
	RequestID      string            `json:"requestId,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	Fields         []CredentialField `json:"fields,omitempty"`
}

// CredentialField describes one credential field being requested from the
// user via the credentials_request SSE event (name + display metadata only —
// never a value or token).
type CredentialField struct {
	Name   string `json:"name"`
	Label  string `json:"label,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// EventSink receives chat events (implemented by the SSE handler).
type EventSink interface {
	Send(ChatEvent) error
	Flush() error
}
