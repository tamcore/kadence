package store

// Scheduled occurrence records: the run rows a task produces, their claim and
// completion transitions, and retention. Task definitions live in
// scheduled_repo.go alongside the repository type.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tamcore/kadence/internal/model"
)

const scheduledTaskRunCols = "id, task_id::text, occurrence_key, scheduled_for, state, started_at, finished_at, result, error, unread, created_at"
const scheduledTaskRunColsQualified = "run.id, run.task_id::text, run.occurrence_key, run.scheduled_for, run.state, run.started_at, run.finished_at, run.result, run.error, run.unread, run.created_at"
const scheduledRunListLimit = 100

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

// ListRuns returns at most 100 immutable occurrence records for one
// non-deleted task owned by userID, newest first.
func (r *ScheduledTaskRepository) ListRuns(ctx context.Context, taskID string, userID int64) ([]model.ScheduledTaskRun, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+scheduledTaskRunColsQualified+` FROM scheduled_task_runs AS run
		 JOIN scheduled_tasks AS task ON task.id = run.task_id
		 WHERE task.id = $1::uuid AND task.user_id = $2 AND task.deleted_at IS NULL
		 ORDER BY run.created_at DESC, run.id DESC
		 LIMIT $3`, taskID, userID, scheduledRunListLimit)
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

// ListRunSummaries returns one latest occurrence and unread count for the same
// bounded priority page as ListByUser.
func (r *ScheduledTaskRepository) ListRunSummaries(ctx context.Context, userID int64, offset, limit int) ([]model.ScheduledTaskRunSummary, error) {
	rows, err := r.pool.Query(ctx,
		`WITH selected_tasks AS (
		        SELECT task.id
		          FROM scheduled_tasks AS task
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
		         LIMIT $2 OFFSET $3
		 )
		 SELECT DISTINCT ON (run.task_id)
		        run.task_id::text,
		        COUNT(*) FILTER (WHERE run.unread) OVER (PARTITION BY run.task_id),
		        `+scheduledTaskRunColsQualified+`
		   FROM scheduled_task_runs AS run
		   JOIN selected_tasks AS task ON task.id = run.task_id
		  ORDER BY run.task_id, run.created_at DESC, run.id DESC`,
		userID, limit, offset, model.ScheduledTaskStateActive, model.ScheduledTaskStatePaused, model.ScheduledTaskStateDraft)
	if err != nil {
		return nil, fmt.Errorf("list scheduled task run summaries: %w", err)
	}
	defer rows.Close()
	summaries := make([]model.ScheduledTaskRunSummary, 0)
	for rows.Next() {
		var summary model.ScheduledTaskRunSummary
		var run model.ScheduledTaskRun
		if err := rows.Scan(&summary.TaskID, &summary.UnreadCount, &run.ID, &run.TaskID, &run.OccurrenceKey,
			&run.ScheduledFor, &run.State, &run.StartedAt, &run.FinishedAt, &run.Result, &run.Error,
			&run.Unread, &run.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan scheduled task run summary: %w", err)
		}
		summary.RecentRun = &run
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list scheduled task run summaries: %w", err)
	}
	return summaries, nil
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
	return inTx(ctx, r.pool, "scheduled manual run", func(tx pgx.Tx) (model.ScheduledTaskRun, error) {
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
		if err := ensureNoScheduledRunInProgress(ctx, tx, taskID); err != nil {
			return model.ScheduledTaskRun{}, err
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
		return run, nil
	})
}

// ClaimDue atomically locks due tasks, creates one running run for each, and
// clears next_run_at. A stopped process therefore never replays a started run.
func (r *ScheduledTaskRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]model.ClaimedScheduledTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	return inTx(ctx, r.pool, "scheduled claim", func(tx pgx.Tx) ([]model.ClaimedScheduledTask, error) {
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
			firstRun := task.LastRunAt == nil
			var username string
			if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, task.UserID).Scan(&username); err != nil {
				return nil, fmt.Errorf("resolve scheduled task owner: %w", err)
			}
			task.LastRunAt = new(run.ScheduledFor)
			task.NextRunAt = nil
			task.UpdatedAt = now
			claimed = append(claimed, model.ClaimedScheduledTask{Task: task, Run: run, FirstRun: firstRun, Username: username})
		}
		return claimed, nil
	})
}

// ListStaleRunning returns started occurrences older than before. It does not
// mutate them: FinishFailure performs the owner-scoped running-state CAS, so
// concurrent replicas may inspect the same stale row but only one can recover
// it.
func (r *ScheduledTaskRepository) ListStaleRunning(ctx context.Context, before time.Time, limit int) ([]model.ClaimedScheduledTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+scheduledTaskRunColsQualified+`, task.user_id, users.username
		 FROM scheduled_task_runs AS run
		 JOIN scheduled_tasks AS task ON task.id = run.task_id
		 JOIN users ON users.id = task.user_id
		 WHERE run.state = $1 AND run.started_at IS NOT NULL AND run.started_at < $2
		 ORDER BY run.started_at
		 LIMIT $3`,
		model.ScheduledTaskRunStateRunning, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale scheduled runs: %w", err)
	}
	type staleRun struct {
		run      model.ScheduledTaskRun
		userID   int64
		username string
	}
	stale := make([]staleRun, 0)
	for rows.Next() {
		var item staleRun
		if err := rows.Scan(
			&item.run.ID, &item.run.TaskID, &item.run.OccurrenceKey, &item.run.ScheduledFor,
			&item.run.State, &item.run.StartedAt, &item.run.FinishedAt, &item.run.Result,
			&item.run.Error, &item.run.Unread, &item.run.CreatedAt, &item.userID, &item.username,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan stale scheduled run: %w", err)
		}
		stale = append(stale, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate stale scheduled runs: %w", err)
	}
	rows.Close()

	claims := make([]model.ClaimedScheduledTask, 0, len(stale))
	for _, item := range stale {
		task, err := scanScheduledTask(r.pool.QueryRow(ctx,
			`SELECT `+scheduledTaskCols+` FROM scheduled_tasks WHERE id = $1::uuid AND user_id = $2`,
			item.run.TaskID, item.userID))
		if err != nil {
			return nil, fmt.Errorf("load stale scheduled task: %w", err)
		}
		claims = append(claims, model.ClaimedScheduledTask{
			Task: task, Run: item.run, Username: item.username,
		})
	}
	return claims, nil
}

// FinishSuccess atomically completes a still-running occurrence, optionally
// inserts its linked assistant delivery, resets failures, persists monitoring
// state, and advances the task.
func (r *ScheduledTaskRepository) FinishSuccess(ctx context.Context, success model.ScheduledExecutionSuccess) error {
	if success.RunState != model.ScheduledTaskRunStateNoChange &&
		success.RunState != model.ScheduledTaskRunStateDelivered &&
		success.RunState != model.ScheduledTaskRunStateCompleted {
		return errors.New("store: invalid scheduled success run state")
	}
	if success.TaskState != model.ScheduledTaskStateActive && success.TaskState != model.ScheduledTaskStateCompleted {
		return errors.New("store: invalid scheduled success task state")
	}
	if len(success.Content) > maxScheduledResultBytes {
		return errors.New("store: scheduled result too large")
	}
	if !json.Valid(success.MonitoringState) || len(success.MonitoringState) > maxScheduledMonitoringStateBytes {
		return errors.New("store: invalid scheduled monitoring state")
	}
	visible := success.RunState == model.ScheduledTaskRunStateDelivered || success.RunState == model.ScheduledTaskRunStateCompleted
	if visible != success.Unread || visible != (success.Content != "") {
		return errors.New("store: inconsistent scheduled delivery")
	}
	return inTxErr(ctx, r.pool, "scheduled success", func(tx pgx.Tx) error {
		var taskID, conversationID, currentTaskState, deliveryConversationID string
		err := tx.QueryRow(ctx,
			`SELECT task.id::text, task.conversation_id::text, task.state,
			        COALESCE(task.delivery_conversation_id::text, task.conversation_id::text)
			 FROM scheduled_task_runs AS run
			 JOIN scheduled_tasks AS task ON task.id = run.task_id
			 WHERE run.id = $1 AND task.user_id = $2 AND run.state = $3
			 FOR UPDATE OF run, task`,
			success.RunID, success.UserID, model.ScheduledTaskRunStateRunning).
			Scan(&taskID, &conversationID, &currentTaskState, &deliveryConversationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock scheduled success: %w", err)
		}
		if success.ConversationID != conversationID {
			return ErrNotFound
		}
		taskState := success.TaskState
		nextRunAt := success.NextRunAt
		if currentTaskState != model.ScheduledTaskStateActive {
			taskState = currentTaskState
			nextRunAt = nil
		}
		if visible {
			var deliveryMessageID int64
			if err := tx.QueryRow(ctx,
				`INSERT INTO messages (conversation_id, role, content, purpose)
				 VALUES ($1::uuid, $2, $3, 'scheduled_delivery')
				 RETURNING id`,
				deliveryConversationID, model.MsgRoleAssistant, success.Content).Scan(&deliveryMessageID); err != nil {
				return fmt.Errorf("insert scheduled delivery: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE conversations SET last_activity_at = GREATEST(last_activity_at, NOW())
				 WHERE id = $1::uuid AND kind = $2`,
				deliveryConversationID, model.ConversationKindChat); err != nil {
				return fmt.Errorf("bump delivery conversation activity: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE scheduled_task_runs SET delivery_message_id = $1 WHERE id = $2`,
				deliveryMessageID, success.RunID); err != nil {
				return fmt.Errorf("record delivery message id: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE scheduled_task_runs SET state = $1, result = $2, unread = $3, finished_at = NOW()
			 WHERE id = $4 AND state = $5`,
			success.RunState, success.Content, success.Unread, success.RunID, model.ScheduledTaskRunStateRunning); err != nil {
			return fmt.Errorf("finish scheduled success run: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET state = $1, monitoring_state = $2::jsonb,
			   consecutive_failures = 0, next_run_at = $3, updated_at = NOW()
			 WHERE id = $4::uuid`,
			taskState, success.MonitoringState, nextRunAt, taskID); err != nil {
			return fmt.Errorf("finish scheduled success task: %w", err)
		}
		return nil
	})
}

// FinishFailure atomically fails only a still-running owner-scoped occurrence,
// stores a bounded public code, increments failures, and advances or pauses the
// task without exposing a partial delivery.
func (r *ScheduledTaskRepository) FinishFailure(ctx context.Context, failure model.ScheduledExecutionFailure) error {
	code := sanitizeScheduledFailureCode(failure.Code)
	if code == "" {
		code = defaultScheduledFailureCode
	}
	return inTxErr(ctx, r.pool, "scheduled execution failure", func(tx pgx.Tx) error {
		var taskID, currentTaskState string
		var failures int
		err := tx.QueryRow(ctx,
			`SELECT task.id::text, task.state, task.consecutive_failures
			 FROM scheduled_task_runs AS run
			 JOIN scheduled_tasks AS task ON task.id = run.task_id
			 WHERE run.id = $1 AND task.user_id = $2 AND run.state = $3
			 FOR UPDATE OF run, task`,
			failure.RunID, failure.UserID, model.ScheduledTaskRunStateRunning).Scan(&taskID, &currentTaskState, &failures)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock scheduled execution failure: %w", err)
		}
		taskState := failure.TaskState
		nextRunAt := failure.NextRunAt
		nextFailures := failures
		if failure.IncrementFailures {
			nextFailures++
		}
		if failure.Pause || (failure.IncrementFailures && nextFailures >= 3) {
			taskState = model.ScheduledTaskStatePaused
			nextRunAt = nil
		}
		if currentTaskState != model.ScheduledTaskStateActive {
			taskState = currentTaskState
			nextRunAt = nil
		}
		if taskState != model.ScheduledTaskStateActive &&
			taskState != model.ScheduledTaskStatePaused &&
			taskState != model.ScheduledTaskStateCompleted &&
			taskState != model.ScheduledTaskStateFailed &&
			taskState != model.ScheduledTaskStateDeleted {
			return errors.New("store: invalid scheduled failure task state")
		}
		if _, err := tx.Exec(ctx,
			`UPDATE scheduled_task_runs SET state = $1, error = $2, finished_at = NOW()
			 WHERE id = $3 AND state = $4`,
			model.ScheduledTaskRunStateFailed, code, failure.RunID, model.ScheduledTaskRunStateRunning); err != nil {
			return fmt.Errorf("finish scheduled failed run: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET consecutive_failures = $1, state = $2, next_run_at = $3, updated_at = NOW()
			 WHERE id = $4::uuid`,
			nextFailures, taskState, nextRunAt, taskID); err != nil {
			return fmt.Errorf("finish scheduled failed task: %w", err)
		}
		return nil
	})
}

// MarkDelivered completes a running run with a user-visible result.
func (r *ScheduledTaskRepository) MarkDelivered(ctx context.Context, runID, userID int64, result string) error {
	return inTxErr(ctx, r.pool, "scheduled delivery", func(tx pgx.Tx) error {
		var taskID string
		err := tx.QueryRow(ctx,
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
		return nil
	})
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
	return inTxErr(ctx, r.pool, "scheduled failure", func(tx pgx.Tx) error {
		var taskID string
		var failures int
		err := tx.QueryRow(ctx,
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
		return nil
	})
}

const (
	defaultScheduledFailureCode      = "execution_failed"
	maxScheduledFailureCodeLen       = 64
	maxScheduledResultBytes          = 64 << 10
	maxScheduledMonitoringStateBytes = 32 << 10
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
