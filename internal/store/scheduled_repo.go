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

// ErrInvalidScheduledTaskState means an atomic lifecycle transition was not
// legal for the task's current persisted state.
var ErrInvalidScheduledTaskState = errors.New("store: invalid scheduled task state")

// ErrStaleScheduledProposal means a CAS proposal transition lost a race or
// referenced a revision that is no longer confirmable.
var ErrStaleScheduledProposal = errors.New("store: scheduled proposal is stale")

// ErrScheduledRunInProgress means a lifecycle change cannot begin until every
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
	"consecutive_failures, next_run_at, last_run_at, created_at, updated_at, deleted_at, " +
	"delivery_conversation_id::text"

func scanScheduledTask(row rowScanner) (model.ScheduledTask, error) {
	var task model.ScheduledTask
	var tools, monitoring []byte
	err := row.Scan(
		&task.ID, &task.UserID, &task.ConversationID, &task.Version, &task.Name, &task.Kind, &task.State,
		&task.CompiledPrompt, &task.OneOffAt, &task.DTStart, &task.RRULE, &task.Timezone, &task.ExecutionMode,
		&tools, &monitoring, &task.DeliveryPolicy, &task.InitialRun, &task.StopCondition, &task.StaticMessage,
		&task.ConsecutiveFailures, &task.NextRunAt, &task.LastRunAt, &task.CreatedAt,
		&task.UpdatedAt, &task.DeletedAt, &task.DeliveryConversationID,
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
	return inTx(ctx, r.pool, "scheduled task create", func(tx pgx.Tx) (model.ScheduledTask, error) {
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
		return created, nil
	})
}

// BeginDraftRevision atomically invalidates any confirmable proposal before
// external compiler work starts. Active and paused definitions may be edited;
// terminal definitions may not.
func (r *ScheduledTaskRepository) BeginDraftRevision(ctx context.Context, id string, userID int64, expectedVersion int) (model.ScheduledTask, error) {
	return inTx(ctx, r.pool, "scheduled draft revision", func(tx pgx.Tx) (model.ScheduledTask, error) {
		current, err := scanScheduledTask(tx.QueryRow(ctx,
			`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
			 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			id, userID))
		if err != nil {
			return model.ScheduledTask{}, err
		}
		if current.Version != expectedVersion {
			return model.ScheduledTask{}, scheduledStaleProposalError()
		}
		if current.State != model.ScheduledTaskStateDraft &&
			current.State != model.ScheduledTaskStateActive &&
			current.State != model.ScheduledTaskStatePaused {
			return model.ScheduledTask{}, ErrInvalidScheduledTaskState
		}
		if err := ensureNoScheduledRunInProgress(ctx, tx, id); err != nil {
			return model.ScheduledTask{}, err
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
		return task, nil
	})
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
	return inTx(ctx, r.pool, "scheduled confirmation", func(tx pgx.Tx) (model.ScheduledTask, error) {
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
			`UPDATE scheduled_tasks SET state = $1, next_run_at = $2,
			   delivery_conversation_id = COALESCE(delivery_conversation_id, conversation_id),
			   updated_at = NOW()
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
		if updated.DeliveryConversationID != nil && *updated.DeliveryConversationID == updated.ConversationID {
			if _, err := tx.Exec(ctx,
				`UPDATE conversations SET kind = $1 WHERE id = $2::uuid AND user_id = $3 AND kind = $4`,
				model.ConversationKindChat, updated.ConversationID, userID, model.ConversationKindScheduled); err != nil {
				return model.ScheduledTask{}, fmt.Errorf("promote scheduled conversation to chat: %w", err)
			}
		}
		return updated, nil
	})
}

// scheduledStaleProposalError is deliberately matched by the service using a
// stable string-free sentinel exposed from the store package.
func scheduledStaleProposalError() error { return ErrStaleScheduledProposal }

// GetByID returns a non-deleted task owned by userID.
func (r *ScheduledTaskRepository) GetByID(ctx context.Context, id string, userID int64) (model.ScheduledTask, error) {
	return scanScheduledTask(r.pool.QueryRow(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL`, id, userID))
}

// ListByUser returns one bounded page with active and unread tasks first.
func (r *ScheduledTaskRepository) ListByUser(ctx context.Context, userID int64, offset, limit int) ([]model.ScheduledTask, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+scheduledTaskCols+` FROM scheduled_tasks AS task
		  WHERE task.user_id = $1 AND task.deleted_at IS NULL
		  ORDER BY CASE
		             WHEN task.state = $4 THEN 0
		             WHEN EXISTS (
		                  SELECT 1 FROM scheduled_task_runs AS unread_run
		                   WHERE unread_run.task_id = task.id AND unread_run.unread
		             ) THEN 1
		             WHEN task.state IN ($5, $6) THEN 2
		             ELSE 3
		           END,
		           task.created_at DESC, task.id DESC
		  LIMIT $2 OFFSET $3`,
		userID, limit, offset, model.ScheduledTaskStateActive, model.ScheduledTaskStatePaused, model.ScheduledTaskStateDraft)
	if err != nil {
		return nil, fmt.Errorf("list scheduled tasks: %w", err)
	}
	return collectRows(rows, "list scheduled tasks", scanScheduledTask)
}

// Pause transitions exactly the expected active revision without writing any
// definition or proposal field.
func (r *ScheduledTaskRepository) Pause(ctx context.Context, id string, userID int64, expectedVersion int) (model.ScheduledTask, error) {
	return inTx(ctx, r.pool, "scheduled pause", func(tx pgx.Tx) (model.ScheduledTask, error) {
		current, err := scanScheduledTask(tx.QueryRow(ctx,
			`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
			 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			id, userID))
		if err != nil {
			return model.ScheduledTask{}, err
		}
		if current.State != model.ScheduledTaskStateActive || current.Version != expectedVersion {
			return model.ScheduledTask{}, ErrInvalidScheduledTaskState
		}
		if err := ensureNoScheduledRunInProgress(ctx, tx, id); err != nil {
			return model.ScheduledTask{}, err
		}
		task, err := scanScheduledTask(tx.QueryRow(ctx,
			`UPDATE scheduled_tasks SET state = $1, next_run_at = NULL, updated_at = NOW()
			 WHERE id = $2::uuid AND user_id = $3 AND deleted_at IS NULL
			   AND state = $4 AND version = $5
			 RETURNING `+scheduledTaskCols,
			model.ScheduledTaskStatePaused, id, userID, model.ScheduledTaskStateActive, expectedVersion))
		if errors.Is(err, ErrNotFound) {
			return model.ScheduledTask{}, ErrInvalidScheduledTaskState
		}
		if err != nil {
			return model.ScheduledTask{}, err
		}
		return task, nil
	})
}

// Resume activates exactly the expected proposal-ready paused revision while
// preserving all definition fields and enforcing the active-task limit.
func (r *ScheduledTaskRepository) Resume(ctx context.Context, id string, userID int64, expectedVersion int, next time.Time) (model.ScheduledTask, error) {
	return inTx(ctx, r.pool, "scheduled resume", func(tx pgx.Tx) (model.ScheduledTask, error) {
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
		return resumed, nil
	})
}

// Update replaces a non-deleted task definition owned by userID. Activating a
// task uses the same owner lock and cap as Create.
func (r *ScheduledTaskRepository) Update(ctx context.Context, task model.ScheduledTask, userID int64) (model.ScheduledTask, error) {
	applyScheduledTaskDefaults(&task)
	tools, monitoring, err := taskJSON(task)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	return inTx(ctx, r.pool, "scheduled task update", func(tx pgx.Tx) (model.ScheduledTask, error) {
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
		return updated, nil
	})
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
	return inTxErr(ctx, r.pool, "scheduled task soft delete", func(tx pgx.Tx) error {
		if _, err := scanScheduledTask(tx.QueryRow(ctx,
			`SELECT `+scheduledTaskCols+` FROM scheduled_tasks
			 WHERE id = $1::uuid AND user_id = $2 AND deleted_at IS NULL FOR UPDATE`,
			id, userID)); err != nil {
			return err
		}
		if err := ensureNoScheduledRunInProgress(ctx, tx, id); err != nil {
			return err
		}
		command, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET state = $1, deleted_at = NOW(), updated_at = NOW()
			 WHERE id = $2::uuid AND user_id = $3 AND deleted_at IS NULL`, model.ScheduledTaskStateDeleted, id, userID)
		if err != nil {
			return fmt.Errorf("soft delete scheduled task: %w", err)
		}
		if command.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// PauseByConversation pauses any live task linked to the owner's Scheduled
// conversation. The conversation itself is intentionally retained: the task
// FK is restrictive so its immutable run audit and definition history survive.
func (r *ScheduledTaskRepository) PauseByConversation(ctx context.Context, conversationID string, userID int64) (bool, error) {
	return inTx(ctx, r.pool, "pause scheduled conversation task", func(tx pgx.Tx) (bool, error) {
		rows, err := tx.Query(ctx,
			`SELECT id::text FROM scheduled_tasks
			 WHERE conversation_id = $1::uuid AND user_id = $2 AND deleted_at IS NULL
			 ORDER BY id FOR UPDATE`,
			conversationID, userID)
		if err != nil {
			return false, fmt.Errorf("find scheduled conversation task: %w", err)
		}
		var taskIDs []string
		for rows.Next() {
			var taskID string
			if err := rows.Scan(&taskID); err != nil {
				rows.Close()
				return false, fmt.Errorf("scan scheduled conversation task: %w", err)
			}
			taskIDs = append(taskIDs, taskID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, fmt.Errorf("find scheduled conversation tasks: %w", err)
		}
		rows.Close()
		if len(taskIDs) == 0 {
			return false, errTxNoop
		}
		for _, taskID := range taskIDs {
			if err := ensureNoScheduledRunInProgress(ctx, tx, taskID); err != nil {
				return false, err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET state = $1, updated_at = NOW()
			 WHERE conversation_id = $2::uuid AND user_id = $3 AND state = $4 AND deleted_at IS NULL`,
			model.ScheduledTaskStatePaused, conversationID, userID, model.ScheduledTaskStateActive); err != nil {
			return false, fmt.Errorf("pause scheduled conversation task: %w", err)
		}
		return true, nil
	})
}

func ensureNoScheduledRunInProgress(ctx context.Context, tx pgx.Tx, taskID string) error {
	var runInProgress bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM scheduled_task_runs
		   WHERE task_id = $1::uuid AND state IN ($2, $3)
		 )`,
		taskID, model.ScheduledTaskRunStatePending, model.ScheduledTaskRunStateRunning).Scan(&runInProgress); err != nil {
		return fmt.Errorf("check scheduled runs before lifecycle change: %w", err)
	}
	if runInProgress {
		return ErrScheduledRunInProgress
	}
	return nil
}
