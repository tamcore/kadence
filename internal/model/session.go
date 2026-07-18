package model

import "time"

// Session is a server-side login session referenced by an opaque cookie value.
type Session struct {
	ID         string
	UserID     int64
	RememberMe bool
	CreatedAt  time.Time
	ExpiresAt  time.Time
}
