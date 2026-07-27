package model

import "time"

// PushSubscription is a browser Web Push subscription owned by a user.
type PushSubscription struct {
	ID            string
	UserID        int64
	Endpoint      string
	P256dh        string
	Auth          string
	UserAgent     string
	CreatedAt     time.Time
	LastSuccessAt *time.Time
	FailureCount  int
}
