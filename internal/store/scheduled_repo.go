package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// ErrInvalidScheduledTaskState means an atomic lifecycle transition was not
// legal for the task's current persisted state.
var ErrInvalidScheduledTaskState = errors.New("store: invalid scheduled task state")

// ErrStaleScheduledProposal means a CAS proposal transition lost a race or
// referenced a revision that is no longer confirmable.
var ErrStaleScheduledProposal = errors.New("store: scheduled proposal is stale")

// ErrScheduledRunInProgress means a draft revision cannot begin until every
// occurrence for the task has reached a terminal state.
var ErrScheduledRunInProgress = errors.New("store: scheduled run is pending or running")

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
	"delivery_policy, initial_run, stop_condition, static_message, " +
	"consecutive_failures, next_run_at, last_run_at, created_at, updated_at, deleted_at"

type rowScanner interface{ Scan(...any) error }

func scanScheduledTask(row rowScanner) (model.ScheduledTask, error) {
	var task model.ScheduledTask
	var tools, monitoring []byte
	err := row.Scan(
		&task.ID, &task.UserID, &task.ConversationID, &task.Version, &task.Name, &task.Kind, &task.State,
		&task.CompiledPrompt, &task.OneOffAt, &task.DTStart, &task.RRULE, &task.Timezone, &task.ExecutionMode,
		&tools, &monitoring, &task.DeliveryPolicy, &task.InitialRun, &task.StopCondition, &task.StaticMessage,
		&task.ConsecutiveFailures, &task.NextRunAt, &task.LastRunAt, &task.CreatedAt,
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

func applyScheduledTaskDefaults(task *model.ScheduledTask) {
	if task.DeliveryPolicy == "" {
		task.DeliveryPolicy = "always"
	}
	if task.InitialRun == "" {
		task.InitialRun = "wait"
	}
}

// Create inserts a scheduled task. A transaction-scoped advisory lock makes
// the active limit safe when requests for the same owner race.
func (r *ScheduledTaskRepository) Create(ctx context.Context, task model.ScheduledTask) (model.ScheduledTask, error) {
	applyScheduledTaskDefaults(&task)
	tools, monitoring, err := taskJSON(task)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if task.Version == 0 && task.State != model.ScheduledTaskStateDraft {
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
			timezone, execution_mode, authorized_tools, monitoring_state, delivery_policy, initial_run, stop_condition, static_message, consecutive_failures, next_run_at, last_run_at
		) VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11, $12, $13::jsonb, $14::jsonb, $15, $16, $17, $18, $19, $20, $21)
		RETURNING `+scheduledTaskCols,
		task.UserID, task.ConversationID, task.Version, task.Name, task.Kind, task.State, task.CompiledPrompt,
		task.OneOffAt, task.DTStart, task.RRULE, task.Timezone, task.ExecutionMode, tools, monitoring,
		task.DeliveryPolicy, task.InitialRun, task.StopCondition, task.StaticMessage, task.ConsecutiveFailures, task.NextRunAt, task.LastRunAt,
	))
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("commit scheduled task create: %w", err)
	}
	return created, nil
}

// BeginDraftRevision atomically invalidates any confirmable proposal before
// external compiler work starts. Active and paused definitions may be edited;
// terminal definitions may not.
func (r *ScheduledTaskRepository) BeginDraftRevision(ctx context.Context, id string, userID int64) (model.ScheduledTask, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.ScheduledTask{}, fmt.Errorf("begin scheduled draft revision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanScheduledTask(tx.QueryRow(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
		 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		id, userID))
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if current.State != model.ScheduledTaskStateDraft &&
		current.State != model.ScheduledTaskStateActive &&
		current.State != model.ScheduledTaskStatePaused {
		return model.ScheduledTask{}, ErrInvalidScheduledTaskState
	}
	var runInProgress bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM scheduled_task_runs
		   WHERE task_id = $1::uuid AND state IN ($2, $3)
		 )`,
		id, model.ScheduledTaskRunStatePending, model.ScheduledTaskRunStateRunning).Scan(&runInProgress); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("check scheduled runs before edit: %w", err)
	}
	if runInProgress {
		return model.ScheduledTask{}, ErrScheduledRunInProgress
	}
	task, err := scanScheduledTask(tx.QueryRow(ctx,
		`UPDATE scheduled_tasks SET
		   version = version + 1, state = $1, name = '', compiled_prompt = '',
		   one_off_at = NULL, dtstart = NULL, rrule = NULL,
		   execution_mode = '', authorized_tools = '[]'::jsonb,
		   delivery_policy = 'always', initial_run = 'wait',
		   stop_condition = '', static_message = '', next_run_at = NULL,
		   updated_at = NOW()
		 WHERE id = $2::uuid AND user_id = $3 AND deleted_at IS NULL
		   AND state = $4 AND version = $5
		 RETURNING `+scheduledTaskCols,
		model.ScheduledTaskStateDraft, id, userID, current.State, current.Version))
	if errors.Is(err, ErrNotFound) {
		return model.ScheduledTask{}, ErrInvalidScheduledTaskState
	}
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("commit scheduled draft revision: %w", err)
	}
	return task, nil
}

// SaveProposal stores compiler output only if the same owner-scoped draft
// revision is still current.
func (r *ScheduledTaskRepository) SaveProposal(ctx context.Context, task model.ScheduledTask, userID int64, expectedVersion int) (model.ScheduledTask, error) {
	applyScheduledTaskDefaults(&task)
	tools, monitoring, err := taskJSON(task)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	updated, err := scanScheduledTask(r.pool.QueryRow(ctx,
		`UPDATE scheduled_tasks SET name = $1, kind = $2, compiled_prompt = $3,
		   one_off_at = $4, dtstart = $5, rrule = NULLIF($6, ''), timezone = $7,
		   execution_mode = $8, authorized_tools = $9::jsonb, monitoring_state = $10::jsonb,
		   delivery_policy = $11, initial_run = $12, stop_condition = $13, static_message = $14,
		   updated_at = NOW()
		 WHERE id = $15::uuid AND user_id = $16 AND deleted_at IS NULL
		   AND state = $17 AND version = $18
		 RETURNING `+scheduledTaskCols,
		task.Name, task.Kind, task.CompiledPrompt, task.OneOffAt, task.DTStart, task.RRULE, task.Timezone,
		task.ExecutionMode, tools, monitoring, task.DeliveryPolicy, task.InitialRun, task.StopCondition,
		task.StaticMessage, task.ID, userID, model.ScheduledTaskStateDraft, expectedVersion))
	if errors.Is(err, ErrNotFound) {
		return model.ScheduledTask{}, scheduledStaleProposalError()
	}
	return updated, err
}

// ConfirmProposal atomically activates exactly the expected owner-scoped draft
// revision while enforcing the per-owner active limit under the owner lock.
func (r *ScheduledTaskRepository) ConfirmProposal(ctx context.Context, id string, userID int64, expectedVersion int, next time.Time) (model.ScheduledTask, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.ScheduledTask{}, fmt.Errorf("begin scheduled confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("lock scheduled task owner: %w", err)
	}
	current, err := scanScheduledTask(tx.QueryRow(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
		 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		id, userID))
	if errors.Is(err, ErrNotFound) {
		return model.ScheduledTask{}, scheduledStaleProposalError()
	}
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if current.State != model.ScheduledTaskStateDraft || current.Version != expectedVersion || current.CompiledPrompt == "" {
		return model.ScheduledTask{}, scheduledStaleProposalError()
	}
	if r.maxActivePerUser > 0 {
		var active int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1 AND state = $2 AND deleted_at IS NULL AND id <> $3::uuid`,
			userID, model.ScheduledTaskStateActive, id).Scan(&active); err != nil {
			return model.ScheduledTask{}, fmt.Errorf("count active scheduled tasks: %w", err)
		}
		if active >= r.maxActivePerUser {
			return model.ScheduledTask{}, ErrActiveTaskLimit
		}
	}
	updated, err := scanScheduledTask(tx.QueryRow(ctx,
		`UPDATE scheduled_tasks SET state = $1, next_run_at = $2, updated_at = NOW()
		 WHERE id = $3::uuid AND user_id = $4 AND deleted_at IS NULL
		   AND state = $5 AND version = $6 AND compiled_prompt <> ''
		 RETURNING `+scheduledTaskCols,
		model.ScheduledTaskStateActive, next, id, userID, model.ScheduledTaskStateDraft, expectedVersion))
	if errors.Is(err, ErrNotFound) {
		return model.ScheduledTask{}, scheduledStaleProposalError()
	}
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("commit scheduled confirmation: %w", err)
	}
	return updated, nil
}

// scheduledStaleProposalError is deliberately matched by the service using a
// stable string-free sentinel exposed from the store package.
func scheduledStaleProposalError() error { return ErrStaleScheduledProposal }

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

// Pause transitions exactly the expected active revision without writing any
// definition or proposal field.
func (r *ScheduledTaskRepository) Pause(ctx context.Context, id string, userID int64, expectedVersion int) (model.ScheduledTask, error) {
	task, err := scanScheduledTask(r.pool.QueryRow(ctx,
		`UPDATE scheduled_tasks SET state = $1, next_run_at = NULL, updated_at = NOW()
		 WHERE id = $2::uuid AND user_id = $3 AND deleted_at IS NULL
		   AND state = $4 AND version = $5
		 RETURNING `+scheduledTaskCols,
		model.ScheduledTaskStatePaused, id, userID, model.ScheduledTaskStateActive, expectedVersion))
	if errors.Is(err, ErrNotFound) {
		return model.ScheduledTask{}, r.scheduledTransitionMiss(ctx, id, userID)
	}
	return task, err
}

// Resume activates exactly the expected proposal-ready paused revision while
// preserving all definition fields and enforcing the active-task limit.
func (r *ScheduledTaskRepository) Resume(ctx context.Context, id string, userID int64, expectedVersion int, next time.Time) (model.ScheduledTask, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.ScheduledTask{}, fmt.Errorf("begin scheduled resume: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("lock scheduled task owner: %w", err)
	}
	current, err := scanScheduledTask(tx.QueryRow(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
		 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		id, userID))
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if current.State != model.ScheduledTaskStatePaused || current.Version != expectedVersion || current.CompiledPrompt == "" {
		return model.ScheduledTask{}, ErrInvalidScheduledTaskState
	}
	if r.maxActivePerUser > 0 {
		var active int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM scheduled_tasks
			 WHERE user_id = $1 AND state = $2 AND deleted_at IS NULL AND id <> $3::uuid`,
			userID, model.ScheduledTaskStateActive, id).Scan(&active); err != nil {
			return model.ScheduledTask{}, fmt.Errorf("count active scheduled tasks: %w", err)
		}
		if active >= r.maxActivePerUser {
			return model.ScheduledTask{}, ErrActiveTaskLimit
		}
	}
	resumed, err := scanScheduledTask(tx.QueryRow(ctx,
		`UPDATE scheduled_tasks SET state = $1, next_run_at = $2, updated_at = NOW()
		 WHERE id = $3::uuid AND user_id = $4 AND deleted_at IS NULL
		   AND state = $5 AND version = $6 AND compiled_prompt <> ''
		 RETURNING `+scheduledTaskCols,
		model.ScheduledTaskStateActive, next, id, userID, model.ScheduledTaskStatePaused, expectedVersion))
	if errors.Is(err, ErrNotFound) {
		return model.ScheduledTask{}, ErrInvalidScheduledTaskState
	}
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTask{}, fmt.Errorf("commit scheduled resume: %w", err)
	}
	return resumed, nil
}

func (r *ScheduledTaskRepository) scheduledTransitionMiss(ctx context.Context, id string, userID int64) error {
	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM scheduled_tasks
		   WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL
		 )`,
		id, userID).Scan(&exists); err != nil {
		return fmt.Errorf("check scheduled lifecycle transition: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrInvalidScheduledTaskState
}

// Update replaces a non-deleted task definition owned by userID. Activating a
// task uses the same owner lock and cap as Create.
func (r *ScheduledTaskRepository) Update(ctx context.Context, task model.ScheduledTask, userID int64) (model.ScheduledTask, error) {
	applyScheduledTaskDefaults(&task)
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
			 delivery_policy = $14, initial_run = $15, stop_condition = $16, static_message = $17,
			 consecutive_failures = $18, next_run_at = $19, last_run_at = $20, updated_at = NOW()
			 WHERE id = $21::uuid AND user_id = $22 AND deleted_at IS NULL RETURNING `+scheduledTaskCols,
		task.ConversationID, task.Version, task.Name, task.Kind, task.State, task.CompiledPrompt, task.OneOffAt,
		task.DTStart, task.RRULE, task.Timezone, task.ExecutionMode, tools, monitoring, task.DeliveryPolicy,
		task.InitialRun, task.StopCondition, task.StaticMessage, task.ConsecutiveFailures, task.NextRunAt,
		task.LastRunAt, task.ID, userID,
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
		`SELECT EXISTS(SELECT 1 FROM conversations WHERE id = $1::uuid AND user_id = $2 AND kind = $3)`,
		conversationID, userID, model.ConversationKindScheduled).Scan(&found)
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

// PauseByConversation pauses any live task linked to the owner's Scheduled
// conversation. The conversation itself is intentionally retained: the task
// FK is restrictive so its immutable run audit and definition history survive.
func (r *ScheduledTaskRepository) PauseByConversation(ctx context.Context, conversationID string, userID int64) (bool, error) {
	var linked bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_tasks WHERE conversation_id = $1::uuid AND user_id = $2 AND deleted_at IS NULL)`, conversationID, userID).Scan(&linked); err != nil {
		return false, fmt.Errorf("find scheduled conversation task: %w", err)
	}
	if !linked {
		return false, nil
	}
	command, err := r.pool.Exec(ctx,
		`UPDATE scheduled_tasks SET state = $1, updated_at = NOW()
		 WHERE conversation_id = $2::uuid AND user_id = $3 AND state = $4 AND deleted_at IS NULL`,
		model.ScheduledTaskStatePaused, conversationID, userID, model.ScheduledTaskStateActive)
	if err != nil {
		return false, fmt.Errorf("pause scheduled conversation task: %w", err)
	}
	_ = command
	return true, nil
}

const scheduledTaskRunCols = "id, task_id::text, occurrence_key, scheduled_for, state, started_at, finished_at, result, error, unread, created_at"
const scheduledTaskRunColsQualified = "run.id, run.task_id::text, run.occurrence_key, run.scheduled_for, run.state, run.started_at, run.finished_at, run.result, run.error, run.unread, run.created_at"

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

// ListRuns returns immutable occurrence records for one non-deleted task
// owned by userID, newest first.
func (r *ScheduledTaskRepository) ListRuns(ctx context.Context, taskID string, userID int64) ([]model.ScheduledTaskRun, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+scheduledTaskRunColsQualified+` FROM scheduled_task_runs AS run
		 JOIN scheduled_tasks AS task ON task.id = run.task_id
		 WHERE task.id = $1::uuid AND task.user_id = $2 AND task.deleted_at IS NULL
		 ORDER BY run.created_at DESC`, taskID, userID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled task runs: %w", err)
	}
	defer rows.Close()
	runs := make([]model.ScheduledTaskRun, 0)
	for rows.Next() {
		run, err := scanScheduledTaskRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		if _, err := r.GetByID(ctx, taskID, userID); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

// CreateRun records an occurrence for a non-deleted task owned by userID. The
// database unique constraint makes a task occurrence immutable and impossible
// to replay accidentally.
func (r *ScheduledTaskRepository) CreateRun(ctx context.Context, userID int64, run model.ScheduledTaskRun) (model.ScheduledTaskRun, error) {
	run.Error = sanitizeScheduledFailureCode(run.Error)
	created, err := scanScheduledTaskRun(r.pool.QueryRow(ctx,
		`INSERT INTO scheduled_task_runs (task_id, occurrence_key, scheduled_for, state, started_at, finished_at, result, error, unread)
		 SELECT task.id, $3, $4, $5, $6, $7, $8, $9, $10
		 FROM scheduled_tasks AS task
		 WHERE task.id = $1::uuid AND task.user_id = $2 AND task.deleted_at IS NULL
		 RETURNING `+scheduledTaskRunCols,
		run.TaskID, userID, run.OccurrenceKey, run.ScheduledFor, run.State, run.StartedAt, run.FinishedAt, run.Result, run.Error, run.Unread))
	if err != nil {
		if isUniqueViolation(err) {
			return model.ScheduledTaskRun{}, ErrOccurrenceTaken
		}
		return model.ScheduledTaskRun{}, err
	}
	return created, nil
}

// RunNow atomically validates and activates an owner-scoped confirmed task,
// creates one pending manual occurrence, and makes that exact occurrence due.
func (r *ScheduledTaskRepository) RunNow(ctx context.Context, userID int64, taskID, occurrenceKey string, now time.Time) (model.ScheduledTaskRun, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.ScheduledTaskRun{}, fmt.Errorf("begin scheduled manual run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return model.ScheduledTaskRun{}, fmt.Errorf("lock scheduled task owner: %w", err)
	}
	task, err := scanScheduledTask(tx.QueryRow(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
		 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		taskID, userID))
	if err != nil {
		return model.ScheduledTaskRun{}, err
	}
	if (task.State != model.ScheduledTaskStateActive && task.State != model.ScheduledTaskStatePaused) || task.CompiledPrompt == "" {
		return model.ScheduledTaskRun{}, ErrInvalidScheduledTaskState
	}
	if task.State == model.ScheduledTaskStatePaused && r.maxActivePerUser > 0 {
		var active int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1 AND state = $2 AND deleted_at IS NULL AND id <> $3::uuid`,
			userID, model.ScheduledTaskStateActive, taskID).Scan(&active); err != nil {
			return model.ScheduledTaskRun{}, fmt.Errorf("count active scheduled tasks: %w", err)
		}
		if active >= r.maxActivePerUser {
			return model.ScheduledTaskRun{}, ErrActiveTaskLimit
		}
	}
	run, err := scanScheduledTaskRun(tx.QueryRow(ctx,
		`INSERT INTO scheduled_task_runs (task_id, occurrence_key, scheduled_for, state)
		 VALUES ($1::uuid, $2, $3, $4) RETURNING `+scheduledTaskRunCols,
		taskID, occurrenceKey, now, model.ScheduledTaskRunStatePending))
	if err != nil {
		if isUniqueViolation(err) {
			return model.ScheduledTaskRun{}, ErrOccurrenceTaken
		}
		return model.ScheduledTaskRun{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE scheduled_tasks SET state = $1, next_run_at = $2, updated_at = $2
		 WHERE id = $3::uuid`,
		model.ScheduledTaskStateActive, now, taskID); err != nil {
		return model.ScheduledTaskRun{}, fmt.Errorf("make manual scheduled run due: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ScheduledTaskRun{}, fmt.Errorf("commit scheduled manual run: %w", err)
	}
	return run, nil
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
			`UPDATE scheduled_task_runs SET state = $1, started_at = $2
			 WHERE id = (
			   SELECT id FROM scheduled_task_runs
			   WHERE task_id = $3::uuid AND state = $4 AND occurrence_key LIKE 'manual:%'
			   ORDER BY created_at LIMIT 1 FOR UPDATE
			 )
			 RETURNING `+scheduledTaskRunCols,
			model.ScheduledTaskRunStateRunning, now, task.ID, model.ScheduledTaskRunStatePending))
		if errors.Is(err, ErrNotFound) {
			run, err = scanScheduledTaskRun(tx.QueryRow(ctx,
				`INSERT INTO scheduled_task_runs (task_id, occurrence_key, scheduled_for, state, started_at)
				 VALUES ($1::uuid, $2, $3, $4, $5) RETURNING `+scheduledTaskRunCols,
				task.ID, task.NextRunAt.UTC().Format(time.RFC3339Nano), *task.NextRunAt, model.ScheduledTaskRunStateRunning, now))
			if err != nil && isUniqueViolation(err) {
				return nil, ErrOccurrenceTaken
			}
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE scheduled_tasks SET last_run_at = next_run_at, next_run_at = NULL, updated_at = $1 WHERE id = $2::uuid`, now, task.ID); err != nil {
			return nil, fmt.Errorf("advance claimed scheduled task: %w", err)
		}
		task.LastRunAt = new(run.ScheduledFor)
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
	command, err := r.pool.Exec(ctx,
		`UPDATE scheduled_task_runs AS run SET unread = FALSE
		 FROM scheduled_tasks AS task WHERE run.task_id = task.id AND task.id = $1::uuid AND task.user_id = $2`, taskID, userID)
	if err != nil {
		return fmt.Errorf("mark scheduled runs read: %w", err)
	}
	if command.RowsAffected() == 0 {
		if _, err := r.GetByID(ctx, taskID, userID); err != nil {
			return err
		}
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
	failure = sanitizeScheduledFailureCode(failure)
	if failure == "" {
		failure = defaultScheduledFailureCode
	}
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

const (
	defaultScheduledFailureCode = "execution_failed"
	maxScheduledFailureCodeLen  = 64
)

func sanitizeScheduledFailureCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if len(code) > maxScheduledFailureCodeLen {
		return defaultScheduledFailureCode
	}
	for _, char := range code {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return defaultScheduledFailureCode
		}
	}
	return code
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
