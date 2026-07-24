package model

import "time"

// Conversation is a chat thread owned by a user.
type Conversation struct {
	ID        string
	UserID    int64
	Title     string
	Kind      string
	CreatedAt time.Time
}
