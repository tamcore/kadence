package model

import "time"

// Chat message roles (distinct from user account roles).
const (
	MsgRoleSystem    = "system"
	MsgRoleUser      = "user"
	MsgRoleAssistant = "assistant"
)

// Message is a single chat message within a conversation.
type Message struct {
	ID             int64
	ConversationID int64
	Role           string
	Content        string
	CreatedAt      time.Time
}
