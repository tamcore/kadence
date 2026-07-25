// Package mcpaudit records LLM-driven remote MCP calls without changing
// dispatch availability when audit persistence is unavailable.
package mcpaudit

import (
	"context"
	"log/slog"
	"time"

	"github.com/tamcore/kadence/internal/model"
)

const (
	startTimeout  = 2 * time.Second
	finishTimeout = 5 * time.Second
)

type Store interface {
	Start(ctx context.Context, call model.MCPAuditCall) (int64, error)
	Finish(ctx context.Context, id int64, status, result, errorText string, finishedAt time.Time) error
}

// Metadata attributes an eventual remote MCP call to its LLM execution.
// SafeArguments carries model-visible placeholder arguments for the direct
// requested tool. Nested remote calls use their actual generated arguments.
type Metadata struct {
	ActorUserID     int64
	ActorUsername   string
	ConversationID  string
	Source          string
	ScheduledTaskID *string
	ScheduledRunID  *int64
	Model           string
	ToolCallID      string
	RequestedTool   string
	SafeArguments   string
	Sanitize        func(string) string
}

type metadataKey struct{}
type persistenceContextKey struct{}

func WithMetadata(ctx context.Context, metadata Metadata) context.Context {
	return context.WithValue(ctx, metadataKey{}, metadata)
}

func MetadataFromContext(ctx context.Context) (Metadata, bool) {
	metadata, ok := ctx.Value(metadataKey{}).(Metadata)
	return metadata, ok
}

// WithPersistenceContext keeps durable audit writes on a caller-owned context
// while the remote invocation itself uses ctx and its tighter deadline.
func WithPersistenceContext(ctx, persistenceCtx context.Context) context.Context {
	return context.WithValue(ctx, persistenceContextKey{}, persistenceCtx)
}

func persistenceContextFrom(ctx context.Context) context.Context {
	persistenceCtx, ok := ctx.Value(persistenceContextKey{}).(context.Context)
	if !ok || persistenceCtx == nil {
		return ctx
	}
	return persistenceCtx
}

type Recorder struct {
	store Store
	log   *slog.Logger
	now   func() time.Time
}

func NewRecorder(store Store, log *slog.Logger, now func() time.Time) *Recorder {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Recorder{store: store, log: log, now: now}
}

// Call records one actual remote MCP dispatch. Audit persistence is fail-open:
// the invocation always proceeds, even when Start or Finish fails.
func (r *Recorder) Call(
	ctx context.Context, toolName, arguments string,
	invoke func(context.Context) (string, error),
) (string, error) {
	metadata, enabled := MetadataFromContext(ctx)
	if r == nil || r.store == nil || !enabled {
		return invoke(ctx)
	}
	persistenceCtx := persistenceContextFrom(ctx)

	auditArguments := arguments
	if metadata.RequestedTool == toolName {
		auditArguments = metadata.SafeArguments
	}
	call := model.MCPAuditCall{
		ActorUserID: metadata.ActorUserID, ActorUsername: metadata.ActorUsername,
		ConversationID: metadata.ConversationID, Source: metadata.Source,
		ScheduledTaskID: metadata.ScheduledTaskID, ScheduledRunID: metadata.ScheduledRunID,
		Model: metadata.Model, ToolCallID: metadata.ToolCallID,
		ToolName: toolName, Arguments: auditArguments,
		Status: model.MCPAuditStatusRunning, StartedAt: r.now(),
	}
	startCtx, cancelStart := context.WithTimeout(persistenceCtx, startTimeout)
	id, startErr := r.store.Start(startCtx, call)
	cancelStart()
	if startErr != nil {
		// TODO(metrics): emit a counter for MCP audit persistence failures.
		r.log.Error("MCP audit start failed", "tool", toolName, "err", startErr)
		return invoke(ctx)
	}

	out, callErr := invoke(ctx)
	status := model.MCPAuditStatusSucceeded
	result, errorText := sanitize(metadata, out), ""
	if callErr != nil {
		status = model.MCPAuditStatusFailed
		result = ""
		errorText = sanitize(metadata, callErr.Error())
	}
	finishCtx, cancelFinish := context.WithTimeout(
		context.WithoutCancel(persistenceCtx), finishTimeout,
	)
	defer cancelFinish()
	if finishErr := r.store.Finish(finishCtx, id, status, result, errorText, r.now()); finishErr != nil {
		// TODO(metrics): emit a counter for MCP audit persistence failures.
		r.log.Error("MCP audit finish failed", "audit_id", id, "tool", toolName, "err", finishErr)
	}
	return out, callErr
}

func sanitize(metadata Metadata, value string) string {
	if metadata.Sanitize != nil {
		return metadata.Sanitize(value)
	}
	return value
}
