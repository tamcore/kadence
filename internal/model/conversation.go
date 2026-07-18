package model

import "time"

// Conversation is a chat thread owned by a user.
type Conversation struct {
	ID        int64
	UserID    int64
	Title     string
	CreatedAt time.Time
}
