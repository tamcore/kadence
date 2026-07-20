package model

import "time"

// Conversation is a chat thread owned by a user.
type Conversation struct {
	ID        string
	UserID    int64
	Title     string
	CreatedAt time.Time
}
