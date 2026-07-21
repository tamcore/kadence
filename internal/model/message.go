package model

import "time"

// Chat message roles (distinct from user account roles).
const (
	MsgRoleSystem    = "system"
	MsgRoleUser      = "user"
	MsgRoleAssistant = "assistant"
)

// MessageToolCall is an audit record of one tool invocation the assistant made
// while producing a message: the tool name and the (redacted) JSON arguments.
type MessageToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is a single chat message within a conversation. ToolCalls is nil
// unless the assistant invoked tools while producing this message.
type Message struct {
	ID             int64
	ConversationID string
	Role           string
	Content        string
	ToolCalls      []MessageToolCall
	CreatedAt      time.Time
}
