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

// PendingPushDelivery is a delivered scheduled run awaiting a push notification.
type PendingPushDelivery struct {
	RunID          int64
	UserID         int64
	TaskID         string
	TaskTitle      string
	ConversationID string
	MessageID      *int64
	Result         string
}
