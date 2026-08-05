package model

import "time"

// SessionIDHash is the sha256 hex digest of a raw session id — the only form
// ever persisted (see store.HashSessionID). It is a distinct type so a hashed
// id cannot be passed to a repository method that expects the raw id and hashes
// its argument: doing so would hash twice, match no row, and silently succeed.
type SessionIDHash string

// Session is a server-side login session referenced by an opaque cookie value.
type Session struct {
	// ID is the RAW session id (the cookie value). It is never persisted: only
	// its hash is. Set by the caller for store.SessionRepository.Create and
	// restored by GetByID. Empty for sessions from ListByUser, which reads the
	// stored hash and cannot recover the raw value — see IDHash.
	ID string
	// IDHash is the persisted sha256 digest of ID. Populated only by
	// store.SessionRepository.ListByUser; compare it against
	// store.HashSessionID(rawCookieValue).
	IDHash     SessionIDHash
	PublicID   string
	UserID     int64
	RememberMe bool
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}
