package model

import (
	"encoding/json"
	"time"
)

// Conversation kind values keep automation-definition history separate from
// ordinary coaching chat.
const (
	ConversationKindChat      = "chat"
	ConversationKindScheduled = "scheduled"
)

// Scheduled task kinds.
const (
	ScheduledTaskKindReminder   = "reminder"
	ScheduledTaskKindData       = "data"
	ScheduledTaskKindMonitoring = "monitoring"
)

// Scheduled task lifecycle states.
const (
	ScheduledTaskStateDraft     = "draft"
	ScheduledTaskStateActive    = "active"
	ScheduledTaskStatePaused    = "paused"
	ScheduledTaskStateCompleted = "completed"
	ScheduledTaskStateFailed    = "failed"
	ScheduledTaskStateDeleted   = "deleted"
)

// Scheduled task run states.
const (
	ScheduledTaskRunStatePending   = "pending"
	ScheduledTaskRunStateRunning   = "running"
	ScheduledTaskRunStateNoChange  = "no_change"
	ScheduledTaskRunStateDelivered = "delivered"
	ScheduledTaskRunStateCompleted = "completed"
	ScheduledTaskRunStateFailed    = "failed"
)

// ScheduledTask is a confirmed or in-progress unattended task definition.
type ScheduledTask struct {
	ID                  string
	UserID              int64
	ConversationID      string
	Version             int
	Name                string
	Kind                string
	State               string
	CompiledPrompt      string
	OneOffAt            *time.Time
	DTStart             *time.Time
	RRULE               string
	Timezone            string
	ExecutionMode       string
	AuthorizedTools     []string
	DeliveryPolicy      string
	InitialRun          string
	StopCondition       string
	StaticMessage       string
	MonitoringState     json.RawMessage
	ConsecutiveFailures int
	NextRunAt           *time.Time
	LastRunAt           *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           *time.Time
}

// ScheduledTaskRun is the durable record for exactly one task occurrence.
type ScheduledTaskRun struct {
	ID            int64
	TaskID        string
	OccurrenceKey string
	ScheduledFor  time.Time
	State         string
	StartedAt     *time.Time
	FinishedAt    *time.Time
	Result        string
	Error         string
	Unread        bool
	CreatedAt     time.Time
}

// ScheduledTaskRunSummary carries the bounded per-task activity needed by
// task lists without fetching every task's full run history.
type ScheduledTaskRunSummary struct {
	TaskID      string
	UnreadCount int
	RecentRun   *ScheduledTaskRun
}

// ClaimedScheduledTask is a task and its atomically-created running occurrence.
type ClaimedScheduledTask struct {
	Task     ScheduledTask
	Run      ScheduledTaskRun
	FirstRun bool
	Username string
}

// ScheduledExecutionSuccess is the atomic persistence request for one
// successful claimed occurrence.
type ScheduledExecutionSuccess struct {
	RunID           int64
	UserID          int64
	ConversationID  string
	RunState        string
	TaskState       string
	Content         string
	Unread          bool
	MonitoringState json.RawMessage
	NextRunAt       *time.Time
}

// ScheduledExecutionFailure is the atomic persistence request for one failed
// claimed occurrence.
type ScheduledExecutionFailure struct {
	RunID             int64
	UserID            int64
	Code              string
	Pause             bool
	IncrementFailures bool
	TaskState         string
	NextRunAt         *time.Time
}
