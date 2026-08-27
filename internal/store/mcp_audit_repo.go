package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

const (
	defaultMCPAuditLimit = 50
	maxMCPAuditLimit     = 100
)

// MCPAuditFilter bounds and filters newest-first audit queries.
type MCPAuditFilter struct {
	Cutoff          time.Time
	From            *time.Time
	To              *time.Time
	ActorUserID     *int64
	ConversationID  string
	Source          string
	Status          string
	Model           string
	Tool            string
	Intent          string
	GuardVerdict    string
	BeforeStartedAt *time.Time
	BeforeID        int64
	Limit           int
}

// MCPAuditRepository stores and queries full MCP call audit records.
type MCPAuditRepository struct{ pool *pgxpool.Pool }

func NewMCPAuditRepository(pool *pgxpool.Pool) *MCPAuditRepository {
	return &MCPAuditRepository{pool: pool}
}

func (r *MCPAuditRepository) Start(ctx context.Context, call model.MCPAuditCall) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO mcp_call_audit (
			actor_user_id, actor_username, conversation_id, source,
			scheduled_task_id, scheduled_run_id, model, tool_call_id,
			tool_name, arguments, intent, guard_verdict, guard_reason,
			status, started_at, finished_at
		) VALUES ($1, $2, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, $11,
			COALESCE(NULLIF($12, ''), 'not_evaluated'), $13,
			COALESCE(NULLIF($14, ''), 'running'), $15, $16)
		RETURNING id`,
		call.ActorUserID, call.ActorUsername, call.ConversationID, call.Source,
		call.ScheduledTaskID, call.ScheduledRunID, call.Model, call.ToolCallID,
		call.ToolName, call.Arguments, call.Intent, call.GuardVerdict, call.GuardReason,
		call.Status, call.StartedAt, call.FinishedAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("start MCP audit: %w", err)
	}
	return id, nil
}

func (r *MCPAuditRepository) Finish(
	ctx context.Context, id int64, status, result, errorText string, finishedAt time.Time,
) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE mcp_call_audit
		   SET status = $2, result = $3, error = $4, finished_at = $5
		 WHERE id = $1 AND status = $6`,
		id, status, result, errorText, finishedAt, model.MCPAuditStatusRunning)
	if err != nil {
		return fmt.Errorf("finish MCP audit: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *MCPAuditRepository) Get(ctx context.Context, id int64, cutoff time.Time) (model.MCPAuditCall, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, actor_user_id, actor_username, conversation_id::text, source,
		       scheduled_task_id::text, scheduled_run_id, model, tool_call_id,
		       tool_name, arguments, intent, guard_verdict, guard_reason,
		       status, result, error, started_at, finished_at
		  FROM mcp_call_audit
		 WHERE id = $1 AND started_at >= $2`, id, cutoff)
	call, err := scanMCPAudit(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.MCPAuditCall{}, ErrNotFound
		}
		return model.MCPAuditCall{}, fmt.Errorf("get MCP audit: %w", err)
	}
	return call, nil
}

func (r *MCPAuditRepository) List(ctx context.Context, f MCPAuditFilter) ([]model.MCPAuditCall, bool, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultMCPAuditLimit
	}
	limit = min(limit, maxMCPAuditLimit)
	rows, err := r.pool.Query(ctx, `
		SELECT id, actor_user_id, actor_username, conversation_id::text, source,
		       scheduled_task_id::text, scheduled_run_id, model, tool_call_id,
		       tool_name, '' AS arguments, intent, guard_verdict, '' AS guard_reason,
		       status, '' AS result, '' AS error,
		       started_at, finished_at
		  FROM mcp_call_audit
		 WHERE started_at >= $1
		   AND ($2::timestamptz IS NULL OR started_at >= $2)
		   AND ($3::timestamptz IS NULL OR started_at <= $3)
		   AND ($4::bigint IS NULL OR actor_user_id = $4)
		   AND ($5 = '' OR conversation_id = NULLIF($5, '')::uuid)
		   AND ($6 = '' OR source = $6)
		   AND ($7 = '' OR status = $7)
		   AND ($8 = '' OR model = $8)
		   AND ($9 = '' OR tool_name = $9)
		   AND ($10 = '' OR intent ILIKE '%' || $10 || '%')
		   AND ($11 = '' OR guard_verdict = $11)
		   AND ($12::timestamptz IS NULL OR (started_at, id) < ($12, $13))
		 ORDER BY started_at DESC, id DESC
		 LIMIT $14`,
		f.Cutoff, f.From, f.To, f.ActorUserID, f.ConversationID,
		f.Source, f.Status, f.Model, f.Tool, f.Intent, f.GuardVerdict,
		f.BeforeStartedAt, f.BeforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list MCP audit: %w", err)
	}
	defer rows.Close()

	out := make([]model.MCPAuditCall, 0, limit)
	for rows.Next() {
		call, scanErr := scanMCPAudit(rows)
		if scanErr != nil {
			return nil, false, fmt.Errorf("scan MCP audit: %w", scanErr)
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list MCP audit rows: %w", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

func (r *MCPAuditRepository) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM mcp_call_audit WHERE started_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired MCP audit: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanMCPAudit(row rowScanner) (model.MCPAuditCall, error) {
	var call model.MCPAuditCall
	err := row.Scan(
		&call.ID, &call.ActorUserID, &call.ActorUsername, &call.ConversationID,
		&call.Source, &call.ScheduledTaskID, &call.ScheduledRunID, &call.Model,
		&call.ToolCallID, &call.ToolName, &call.Arguments, &call.Intent,
		&call.GuardVerdict, &call.GuardReason, &call.Status, &call.Result,
		&call.Error, &call.StartedAt, &call.FinishedAt,
	)
	return call, err
}
