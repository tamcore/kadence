package model

import "time"

const (
	MCPAuditSourceChat      = "chat"
	MCPAuditSourceScheduled = "scheduled"

	MCPAuditStatusRunning   = "running"
	MCPAuditStatusSucceeded = "succeeded"
	MCPAuditStatusFailed    = "failed"
)

// MCPAuditCall records one remote MCP invocation selected by an LLM.
// Identity fields intentionally remain snapshots rather than foreign keys so
// short-lived audit records survive user/chat/task deletion until their TTL.
type MCPAuditCall struct {
	ID              int64
	ActorUserID     int64
	ActorUsername   string
	ConversationID  string
	Source          string
	ScheduledTaskID *string
	ScheduledRunID  *int64
	Model           string
	ToolCallID      string
	ToolName        string
	Arguments       string
	Status          string
	Result          string
	Error           string
	StartedAt       time.Time
	FinishedAt      *time.Time
}
