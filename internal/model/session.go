package model

import "time"

// Session is a server-side login session referenced by an opaque cookie value.
type Session struct {
	// ID is the session identifier. Only its sha256 hash is ever persisted
	// (see store.HashSessionID); depending on how the Session was obtained,
	// this field holds either the raw value (store.SessionRepository.Create,
	// GetByID) or the stored hash (ListByUser, which cannot recover the raw
	// value). See each method's doc comment.
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
