package model

import "time"

// Scheduled handoff artifact states describe the chat-visible progress of a
// draft created from an ordinary conversation turn.
const (
	ScheduledHandoffStateCreating  = "creating"
	ScheduledHandoffStateReady     = "ready"
	ScheduledHandoffStateFailed    = "failed"
	ScheduledHandoffStateDismissed = "dismissed"
)

// ScheduledHandoff links one idempotent ordinary-chat invocation slot to its
// Scheduled task. Confirmed task records may outlive the source chat.
type ScheduledHandoff struct {
	ID                       string
	UserID                   int64
	SourceConversationID     string
	SourceUserMessageID      int64
	SourceContentFingerprint []byte
	AssistantMessageID       *int64
	ScheduledTaskID          *string
	InvocationOrdinal        int
	ArtifactState            string
	ErrorCode                string
	Retryable                bool
	CreatedAt                time.Time
	UpdatedAt                time.Time
}
