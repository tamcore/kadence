package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// ErrActiveTaskLimit means creating or activating a task would exceed the
// configured per-user active-task limit.
var ErrActiveTaskLimit = errors.New("store: scheduled active task limit reached")

// ErrOccurrenceTaken means a task already has a run for that occurrence key.
var ErrOccurrenceTaken = errors.New("store: scheduled occurrence already exists")

// ScheduledTaskRepository persists owner-scoped scheduled tasks and runs.
type ScheduledTaskRepository struct {
	pool             *pgxpool.Pool
	maxActivePerUser int
}

// NewScheduledTaskRepository returns a repository using maxActivePerUser as
// the per-owner active-task cap.
func NewScheduledTaskRepository(pool *pgxpool.Pool, maxActivePerUser int) *ScheduledTaskRepository {
	return &ScheduledTaskRepository{pool: pool, maxActivePerUser: maxActivePerUser}
}

const scheduledTaskCols = "id::text, user_id, conversation_id::text, version, name, kind, state, compiled_prompt, " +
	"one_off_at, dtstart, COALESCE(rrule, ''), timezone, execution_mode, authorized_tools, monitoring_state, " +
	"consecutive_failures, next_run_at, last_run_at, created_at, updated_at, deleted_at"

type rowScanner interface{ Scan(...any) error }

func scanScheduledTask(row rowScanner) (model.ScheduledTask, error) {
	var task model.ScheduledTask
	var tools, monitoring []byte
	err := row.Scan(
		&task.ID, &task.UserID, &task.ConversationID, &task.Version, &task.Name, &task.Kind, &task.State,
		&task.CompiledPrompt, &task.OneOffAt, &task.DTStart, &task.RRULE, &task.Timezone, &task.ExecutionMode,
		&tools, &monitoring, &task.ConsecutiveFailures, &task.NextRunAt, &task.LastRunAt, &task.CreatedAt,
		&task.UpdatedAt, &task.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ScheduledTask{}, ErrNotFound
	}
	if err != nil {
		return model.ScheduledTask{}, fmt.Errorf("scan scheduled task: %w", err)
	}
	if err := json.Unmarshal(tools, &task.AuthorizedTools); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("decode scheduled task tools: %w", err)
	}
	task.MonitoringState = append(task.MonitoringState[:0], monitoring...)
	return task, nil
}

func taskJSON(task model.ScheduledTask) ([]byte, []byte, error) {
	tools := task.AuthorizedTools
	if tools == nil {
		tools = []string{}
	}
	monitoring := task.MonitoringState
	if len(monitoring) == 0 {
		monitoring = json.RawMessage(`{}`)
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return nil, nil, fmt.Errorf("encode scheduled task tools: %w", err)
	}
	if !json.Valid(monitoring) {
		return nil, nil, errors.New("scheduled task monitoring state must be valid JSON")
	}
	return toolsJSON, monitoring, nil
}

// Create inserts a scheduled task. A transaction-scoped advisory lock makes
// the active limit safe when requests for the same owner race.
func (r *ScheduledTaskRepository) Create(ctx context.Context, task model.ScheduledTask) (model.ScheduledTask, error) {
	tools, monitoring, err := taskJSON(task)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if task.Version == 0 {
		task.Version = 1
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.ScheduledTask{}, fmt.Errorf("begin scheduled task create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, task.UserID); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("lock scheduled task owner: %w", err)
	}
	if err := ensureOwnedScheduledConversation(ctx, tx, task.ConversationID, task.UserID); err != nil {
		return model.ScheduledTask{}, err
	}
	if task.State == model.ScheduledTaskStateActive && r.maxActivePerUser > 0 {
		var active int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1 AND state = $2 AND deleted_at IS NULL`, task.UserID, model.ScheduledTaskStateActive).Scan(&active); err != nil {
			return model.ScheduledTask{}, fmt.Errorf("count active scheduled tasks: %w", err)
		}
		if active >= r.maxActivePerUser {
			return model.ScheduledTask{}, ErrActiveTaskLimit
		}
	}
	created, err := scanScheduledTask(tx.QueryRow(ctx,
		`INSERT INTO scheduled_tasks (
			user_id, conversation_id, version, name, kind, state, compiled_prompt, one_off_at, dtstart, rrule,
			timezone, execution_mode, authorized_tools, monitoring_state, consecutive_failures, next_run_at, last_run_at
		) VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11, $12, $13::jsonb, $14::jsonb, $15, $16, $17)
		RETURNING `+scheduledTaskCols,
		task.UserID, task.ConversationID, task.Version, task.Name, task.Kind, task.State, task.CompiledPrompt,
		task.OneOffAt, task.DTStart, task.RRULE, task.Timezone, task.ExecutionMode, tools, monitoring,
		task.ConsecutiveFailures, task.NextRunAt, task.LastRunAt,
	))
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("commit scheduled task create: %w", err)
	}
	return created, nil
}

// GetByID returns a non-deleted task owned by userID.
func (r *ScheduledTaskRepository) GetByID(ctx context.Context, id string, userID int64) (model.ScheduledTask, error) {
	return scanScheduledTask(r.pool.QueryRow(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL`, id, userID))
}

// ListByUser returns a user's non-deleted tasks, newest first.
func (r *ScheduledTaskRepository) ListByUser(ctx context.Context, userID int64) ([]model.ScheduledTask, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+scheduledTaskCols+` FROM scheduled_tasks WHERE user_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled tasks: %w", err)
	}
	defer rows.Close()
	var tasks []model.ScheduledTask
	for rows.Next() {
		task, err := scanScheduledTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// Update replaces a non-deleted task definition owned by userID. Activating a
// task uses the same owner lock and cap as Create.
func (r *ScheduledTaskRepository) Update(ctx context.Context, task model.ScheduledTask, userID int64) (model.ScheduledTask, error) {
	tools, monitoring, err := taskJSON(task)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.ScheduledTask{}, fmt.Errorf("begin scheduled task update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("lock scheduled task owner: %w", err)
	}
	if err := ensureOwnedScheduledConversation(ctx, tx, task.ConversationID, userID); err != nil {
		return model.ScheduledTask{}, err
	}
	if task.State == model.ScheduledTaskStateActive && r.maxActivePerUser > 0 {
		var active int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1 AND state = $2 AND deleted_at IS NULL AND id <> $3::uuid`,
			userID, model.ScheduledTaskStateActive, task.ID).Scan(&active); err != nil {
			return model.ScheduledTask{}, fmt.Errorf("count active scheduled tasks: %w", err)
		}
		if active >= r.maxActivePerUser {
			return model.ScheduledTask{}, ErrActiveTaskLimit
		}
	}
	updated, err := scanScheduledTask(tx.QueryRow(ctx,
		`UPDATE scheduled_tasks SET conversation_id = $1::uuid, version = $2, name = $3, kind = $4, state = $5,
		 compiled_prompt = $6, one_off_at = $7, dtstart = $8, rrule = NULLIF($9, ''), timezone = $10,
		 execution_mode = $11, authorized_tools = $12::jsonb, monitoring_state = $13::jsonb,
		 consecutive_failures = $14, next_run_at = $15, last_run_at = $16, updated_at = NOW()
		 WHERE id = $17::uuid AND user_id = $18 AND deleted_at IS NULL RETURNING `+scheduledTaskCols,
		task.ConversationID, task.Version, task.Name, task.Kind, task.State, task.CompiledPrompt, task.OneOffAt,
		task.DTStart, task.RRULE, task.Timezone, task.ExecutionMode, tools, monitoring, task.ConsecutiveFailures,
		task.NextRunAt, task.LastRunAt, task.ID, userID,
	))
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("commit scheduled task update: %w", err)
	}
	return updated, nil
}

func ensureOwnedScheduledConversation(ctx context.Context, tx pgx.Tx, conversationID string, userID int64) error {
	var found bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM conversations WHERE id = $1::uuid AND user_id = $2)`, conversationID, userID).Scan(&found)
	if err != nil {
		return fmt.Errorf("check scheduled task conversation owner: %w", err)
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// SoftDelete marks a task deleted while preserving its audit records.
func (r *ScheduledTaskRepository) SoftDelete(ctx context.Context, id string, userID int64) error {
	command, err := r.pool.Exec(ctx,
		`UPDATE scheduled_tasks SET state = $1, deleted_at = NOW(), updated_at = NOW()
		 WHERE id = $2::uuid AND user_id = $3 AND deleted_at IS NULL`, model.ScheduledTaskStateDeleted, id, userID)
	if err != nil {
		return fmt.Errorf("soft delete scheduled task: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const scheduledTaskRunCols = "id, task_id::text, occurrence_key, scheduled_for, state, started_at, finished_at, result, error, unread, created_at"

func scanScheduledTaskRun(row rowScanner) (model.ScheduledTaskRun, error) {
	var run model.ScheduledTaskRun
	err := row.Scan(&run.ID, &run.TaskID, &run.OccurrenceKey, &run.ScheduledFor, &run.State, &run.StartedAt,
		&run.FinishedAt, &run.Result, &run.Error, &run.Unread, &run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ScheduledTaskRun{}, ErrNotFound
	}
	if err != nil {
		return model.ScheduledTaskRun{}, fmt.Errorf("scan scheduled task run: %w", err)
	}
	return run, nil
}

// CreateRun records an occurrence. The database unique constraint makes a
// task occurrence immutable and impossible to replay accidentally.
func (r *ScheduledTaskRepository) CreateRun(ctx context.Context, run model.ScheduledTaskRun) (model.ScheduledTaskRun, error) {
	created, err := scanScheduledTaskRun(r.pool.QueryRow(ctx,
		`INSERT INTO scheduled_task_runs (task_id, occurrence_key, scheduled_for, state, started_at, finished_at, result, error, unread)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+scheduledTaskRunCols,
		run.TaskID, run.OccurrenceKey, run.ScheduledFor, run.State, run.StartedAt, run.FinishedAt, run.Result, run.Error, run.Unread))
	if err != nil {
		if isUniqueViolation(err) {
			return model.ScheduledTaskRun{}, ErrOccurrenceTaken
		}
		return model.ScheduledTaskRun{}, err
	}
	return created, nil
}

// ClaimDue atomically locks due tasks, creates one running run for each, and
// clears next_run_at. A stopped process therefore never replays a started run.
func (r *ScheduledTaskRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]model.ClaimedScheduledTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin scheduled claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
		 WHERE state = $1 AND deleted_at IS NULL AND next_run_at <= $2
		 ORDER BY next_run_at FOR UPDATE SKIP LOCKED LIMIT $3`, model.ScheduledTaskStateActive, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due scheduled tasks: %w", err)
	}
	defer rows.Close()
	var due []model.ScheduledTask
	for rows.Next() {
		task, err := scanScheduledTask(rows)
		if err != nil {
			return nil, err
		}
		due = append(due, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	claimed := make([]model.ClaimedScheduledTask, 0, len(due))
	for _, task := range due {
		if task.NextRunAt == nil {
			return nil, errors.New("scheduled: due task has no next run")
		}
		run, err := scanScheduledTaskRun(tx.QueryRow(ctx,
			`INSERT INTO scheduled_task_runs (task_id, occurrence_key, scheduled_for, state, started_at)
			 VALUES ($1::uuid, $2, $3, $4, $5) RETURNING `+scheduledTaskRunCols,
			task.ID, task.NextRunAt.UTC().Format(time.RFC3339Nano), *task.NextRunAt, model.ScheduledTaskRunStateRunning, now))
		if err != nil {
			if isUniqueViolation(err) {
				return nil, ErrOccurrenceTaken
			}
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE scheduled_tasks SET last_run_at = next_run_at, next_run_at = NULL, updated_at = $1 WHERE id = $2::uuid`, now, task.ID); err != nil {
			return nil, fmt.Errorf("advance claimed scheduled task: %w", err)
		}
		task.LastRunAt = task.NextRunAt
		task.NextRunAt = nil
		task.UpdatedAt = now
		claimed = append(claimed, model.ClaimedScheduledTask{Task: task, Run: run})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit scheduled claim: %w", err)
	}
	return claimed, nil
}

// MarkDelivered completes a running run with a user-visible result.
func (r *ScheduledTaskRepository) MarkDelivered(ctx context.Context, runID, userID int64, result string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin scheduled delivery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID string
	err = tx.QueryRow(ctx,
		`UPDATE scheduled_task_runs AS run SET state = $1, result = $2, unread = TRUE, finished_at = NOW()
		 FROM scheduled_tasks AS task
		 WHERE run.id = $3 AND run.task_id = task.id AND task.user_id = $4 AND run.state = $5
		 RETURNING task.id::text`,
		model.ScheduledTaskRunStateDelivered, result, runID, userID, model.ScheduledTaskRunStateRunning).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("mark scheduled delivery: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE scheduled_tasks SET consecutive_failures = 0, updated_at = NOW() WHERE id = $1::uuid`, taskID); err != nil {
		return fmt.Errorf("reset scheduled task failures: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit scheduled delivery: %w", err)
	}
	return nil
}

// MarkRead clears unread delivery state for all of one owner's task runs.
func (r *ScheduledTaskRepository) MarkRead(ctx context.Context, taskID string, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE scheduled_task_runs AS run SET unread = FALSE
		 FROM scheduled_tasks AS task WHERE run.task_id = task.id AND task.id = $1::uuid AND task.user_id = $2`, taskID, userID)
	if err != nil {
		return fmt.Errorf("mark scheduled runs read: %w", err)
	}
	return nil
}

// UnreadCount returns the number of unread deliveries belonging to userID.
func (r *ScheduledTaskRepository) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM scheduled_task_runs AS run JOIN scheduled_tasks AS task ON task.id = run.task_id
		 WHERE task.user_id = $1 AND task.deleted_at IS NULL AND run.unread`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread scheduled runs: %w", err)
	}
	return count, nil
}

// RecordFailure transitions a running occurrence to failed and pauses its
// task after the third consecutive failure.
func (r *ScheduledTaskRepository) RecordFailure(ctx context.Context, runID, userID int64, failure string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin scheduled failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID string
	var failures int
	err = tx.QueryRow(ctx,
		`SELECT task.id::text, task.consecutive_failures FROM scheduled_task_runs AS run
		 JOIN scheduled_tasks AS task ON task.id = run.task_id
		 WHERE run.id = $1 AND task.user_id = $2 AND run.state = $3 FOR UPDATE OF task`,
		runID, userID, model.ScheduledTaskRunStateRunning).Scan(&taskID, &failures)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock scheduled failure task: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE scheduled_task_runs SET state = $1, error = $2, finished_at = NOW() WHERE id = $3`, model.ScheduledTaskRunStateFailed, failure, runID); err != nil {
		return fmt.Errorf("fail scheduled run: %w", err)
	}
	state := model.ScheduledTaskStateActive
	if failures+1 >= 3 {
		state = model.ScheduledTaskStatePaused
	}
	if _, err := tx.Exec(ctx, `UPDATE scheduled_tasks SET consecutive_failures = $1, state = $2, updated_at = NOW() WHERE id = $3::uuid`, failures+1, state, taskID); err != nil {
		return fmt.Errorf("record scheduled task failure: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit scheduled failure: %w", err)
	}
	return nil
}

// DeleteExpiredNoChange removes no-change run records older than before.
func (r *ScheduledTaskRepository) DeleteExpiredNoChange(ctx context.Context, before time.Time) (int64, error) {
	command, err := r.pool.Exec(ctx,
		`DELETE FROM scheduled_task_runs WHERE state = $1 AND finished_at IS NOT NULL AND finished_at < $2`, model.ScheduledTaskRunStateNoChange, before)
	if err != nil {
		return 0, fmt.Errorf("delete expired scheduled no-change runs: %w", err)
	}
	return command.RowsAffected(), nil
}
