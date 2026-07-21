package model

import "time"

// Session is a server-side login session referenced by an opaque cookie value.
type Session struct {
	ID         string
	PublicID   string
	UserID     int64
	RememberMe bool
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}
