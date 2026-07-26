package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// CreateChatHandoffInput identifies an idempotent source invocation and
// supplies the initial Scheduled draft metadata.
type CreateChatHandoffInput struct {
	UserID                   int64
	SourceConversationID     string
	SourceUserMessageID      int64
	SourceContentFingerprint []byte
	InvocationOrdinal        int
	Title                    string
	Timezone                 string
}

// HydratedChatHandoff contains the card's durable linkage and its current
// Scheduled task and canonical latest definition response.
type HydratedChatHandoff struct {
	Handoff                   model.ScheduledHandoff
	Task                      *model.ScheduledTask
	LatestDefinitionAssistant string
}

// ScheduledHandoffRepository persists chat-created Scheduled drafts.
type ScheduledHandoffRepository struct{ pool *pgxpool.Pool }

// NewScheduledHandoffRepository returns a repository backed by pool.
func NewScheduledHandoffRepository(pool *pgxpool.Pool) *ScheduledHandoffRepository {
	return &ScheduledHandoffRepository{pool: pool}
}

const handoffCols = "h.id::text, h.user_id, h.source_conversation_id::text, h.source_user_message_id, " +
	"h.source_content_fingerprint, h.assistant_message_id, h.scheduled_task_id::text, h.invocation_ordinal, " +
	"h.artifact_state, h.error_code, h.retryable, h.created_at, h.updated_at"

// CreateOrGetDraft reserves the source slot before creating its dependent
// Scheduled conversation and draft. Concurrent callers reuse the winner.
func (r *ScheduledHandoffRepository) CreateOrGetDraft(
	ctx context.Context, in CreateChatHandoffInput,
) (HydratedChatHandoff, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return HydratedChatHandoff{}, false, fmt.Errorf("begin create chat handoff: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceOwned bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM conversations AS conversation
			JOIN messages AS message ON message.conversation_id = conversation.id
			 WHERE conversation.id = $1::uuid AND conversation.user_id = $2 AND conversation.kind = $3
			   AND message.id = $4 AND message.role = $5 AND message.purpose = $6
		)`,
		in.SourceConversationID, in.UserID, model.ConversationKindChat, in.SourceUserMessageID,
		model.MsgRoleUser, messagePurposeChat,
	).Scan(&sourceOwned); err != nil {
		return HydratedChatHandoff{}, false, fmt.Errorf("check chat handoff source owner: %w", err)
	}
	if !sourceOwned {
		return HydratedChatHandoff{}, false, ErrNotFound
	}

	var handoffID string
	err = tx.QueryRow(ctx,
		`INSERT INTO chat_scheduled_handoffs (
			user_id, source_conversation_id, source_user_message_id,
			source_content_fingerprint, invocation_ordinal, artifact_state
		) VALUES ($1, $2::uuid, $3, $4, $5, 'creating')
		ON CONFLICT (source_user_message_id, source_content_fingerprint, invocation_ordinal)
		DO NOTHING
		RETURNING id::text`,
		in.UserID, in.SourceConversationID, in.SourceUserMessageID, in.SourceContentFingerprint, in.InvocationOrdinal,
	).Scan(&handoffID)
	if err == nil {
		var scheduledConversationID, taskID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO conversations (user_id, title, kind) VALUES ($1, $2, $3) RETURNING id::text`,
			in.UserID, in.Title, model.ConversationKindScheduled,
		).Scan(&scheduledConversationID); err != nil {
			return HydratedChatHandoff{}, false, fmt.Errorf("create handoff scheduled conversation: %w", err)
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO scheduled_tasks (user_id, conversation_id, name, kind, state, timezone)
			 VALUES ($1, $2::uuid, $3, $4, $5, $6) RETURNING id::text`,
			in.UserID, scheduledConversationID, in.Title, model.ScheduledTaskKindReminder, model.ScheduledTaskStateDraft, in.Timezone,
		).Scan(&taskID); err != nil {
			return HydratedChatHandoff{}, false, fmt.Errorf("create handoff draft task: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE chat_scheduled_handoffs SET scheduled_task_id = $1::uuid, updated_at = NOW() WHERE id = $2::uuid`,
			taskID, handoffID,
		); err != nil {
			return HydratedChatHandoff{}, false, fmt.Errorf("attach handoff draft task: %w", err)
		}
		row, err := getHandoffByID(ctx, tx, in.UserID, handoffID)
		if err != nil {
			return HydratedChatHandoff{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return HydratedChatHandoff{}, false, fmt.Errorf("commit create chat handoff: %w", err)
		}
		return row, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HydratedChatHandoff{}, false, fmt.Errorf("reserve chat handoff slot: %w", err)
	}
	row, err := getHandoffBySlot(ctx, tx, in)
	if err != nil {
		return HydratedChatHandoff{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return HydratedChatHandoff{}, false, fmt.Errorf("commit load chat handoff: %w", err)
	}
	return row, false, nil
}

// MarkTaskReady marks the owner's handoff card ready after its draft was
// persisted by the Scheduled compiler.
func (r *ScheduledHandoffRepository) MarkTaskReady(ctx context.Context, userID int64, taskID string) error {
	command, err := r.pool.Exec(ctx,
		`UPDATE chat_scheduled_handoffs SET artifact_state = $1, error_code = '', retryable = FALSE, updated_at = NOW()
		 WHERE user_id = $2 AND scheduled_task_id = $3::uuid AND artifact_state IN ($4, $5, $6)`,
		model.ScheduledHandoffStateReady, userID, taskID, model.ScheduledHandoffStateCreating,
		model.ScheduledHandoffStateFailed, model.ScheduledHandoffStateReady,
	)
	if err != nil {
		return fmt.Errorf("mark chat handoff ready: %w", err)
	}
	return handoffTaskUpdateResult(ctx, r.pool, userID, taskID, command.RowsAffected())
}

// MarkTaskFailed records a safe, retryable or terminal compiler failure.
func (r *ScheduledHandoffRepository) MarkTaskFailed(
	ctx context.Context, userID int64, taskID, errorCode string, retryable bool,
) error {
	command, err := r.pool.Exec(ctx,
		`UPDATE chat_scheduled_handoffs SET artifact_state = $1, error_code = $2, retryable = $3, updated_at = NOW()
		 WHERE user_id = $4 AND scheduled_task_id = $5::uuid AND artifact_state IN ($6, $7, $8)`,
		model.ScheduledHandoffStateFailed, errorCode, retryable, userID, taskID, model.ScheduledHandoffStateCreating,
		model.ScheduledHandoffStateReady, model.ScheduledHandoffStateFailed,
	)
	if err != nil {
		return fmt.Errorf("mark chat handoff failed: %w", err)
	}
	return handoffTaskUpdateResult(ctx, r.pool, userID, taskID, command.RowsAffected())
}

func handoffTaskUpdateResult(ctx context.Context, pool *pgxpool.Pool, userID int64, taskID string, affected int64) error {
	if affected == 1 {
		return nil
	}
	var owned bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_tasks WHERE id = $1::uuid AND user_id = $2)`, taskID, userID).Scan(&owned); err != nil {
		return fmt.Errorf("check chat handoff task owner: %w", err)
	}
	if owned {
		return nil
	}
	return ErrNotFound
}

// ListByAssistantMessages batch-loads the cards placed in source messages.
// Its lateral join selects the canonical latest definition assistant response.
func (r *ScheduledHandoffRepository) ListByAssistantMessages(
	ctx context.Context, userID int64, conversationID string, messageIDs []int64,
) ([]HydratedChatHandoff, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, handoffHydrationQuery+`
	 WHERE h.user_id = $1 AND h.source_conversation_id = $2::uuid
	   AND h.assistant_message_id = ANY($3)
	 ORDER BY h.assistant_message_id, h.invocation_ordinal`, userID, conversationID, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("list chat handoffs by assistant messages: %w", err)
	}
	defer rows.Close()
	var out []HydratedChatHandoff
	for rows.Next() {
		row, err := scanHydratedHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListPendingBySourceConversation returns at most two owner-scoped,
// assistant-bound unresolved drafts. Two rows are enough for callers to
// distinguish a sole draft from multiple pending tasks.
func (r *ScheduledHandoffRepository) ListPendingBySourceConversation(
	ctx context.Context, userID int64, conversationID string,
) ([]HydratedChatHandoff, error) {
	rows, err := r.pool.Query(ctx, handoffHydrationQuery+`
	 WHERE h.user_id = $1 AND h.source_conversation_id = $2::uuid
	   AND h.assistant_message_id IS NOT NULL
	   AND h.artifact_state <> $3
	   AND task.user_id = $1 AND task.state = $4
	 ORDER BY h.created_at, h.invocation_ordinal
	 LIMIT 2`,
		userID, conversationID, model.ScheduledHandoffStateDismissed, model.ScheduledTaskStateDraft,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending chat handoffs: %w", err)
	}
	defer rows.Close()
	var out []HydratedChatHandoff
	for rows.Next() {
		row, err := scanHydratedHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DiscardDraft deletes one unconfirmed Scheduled draft and its definition
// conversation, retaining a durable dismissed source-slot tombstone.
func (r *ScheduledHandoffRepository) DiscardDraft(ctx context.Context, userID int64, taskID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin discard chat handoff draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := discardDraftTask(ctx, tx, userID, taskID, true); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit discard chat handoff draft: %w", err)
	}
	return nil
}

// CleanupDrafts hard-deletes only supplied owner-scoped draft artifacts. It
// deliberately leaves dismissed tombstones and confirmed/non-draft tasks.
func (r *ScheduledHandoffRepository) CleanupDrafts(ctx context.Context, userID int64, handoffIDs []string) error {
	if len(handoffIDs) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cleanup chat handoff drafts: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := cleanupDraftHandoffs(ctx, tx, userID, handoffIDs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup chat handoff drafts: %w", err)
	}
	return nil
}

const handoffHydrationQuery = `SELECT ` + handoffCols + `,
       task.id IS NOT NULL,
       COALESCE(task.id::text, ''), COALESCE(task.user_id, 0), COALESCE(task.conversation_id::text, ''),
       COALESCE(task.version, 0), COALESCE(task.name, ''), COALESCE(task.kind, ''), COALESCE(task.state, ''),
       COALESCE(task.compiled_prompt, ''), task.one_off_at, task.dtstart, COALESCE(task.rrule, ''),
       COALESCE(task.timezone, ''), COALESCE(task.execution_mode, ''), COALESCE(task.authorized_tools, '[]'::jsonb),
       COALESCE(task.monitoring_state, '{}'::jsonb), COALESCE(task.delivery_policy, ''), COALESCE(task.initial_run, ''),
       COALESCE(task.stop_condition, ''), COALESCE(task.static_message, ''), COALESCE(task.consecutive_failures, 0),
	       task.next_run_at, task.last_run_at, COALESCE(task.created_at, NOW()), COALESCE(task.updated_at, NOW()), task.deleted_at,
       COALESCE(latest.content, '')
  FROM chat_scheduled_handoffs AS h
  LEFT JOIN scheduled_tasks AS task ON task.id = h.scheduled_task_id
  LEFT JOIN LATERAL (
       SELECT content FROM messages
        WHERE conversation_id = task.conversation_id
          AND role = 'assistant' AND purpose = 'scheduled_definition'
        ORDER BY id DESC LIMIT 1
  ) AS latest ON TRUE`

type handoffRowScanner interface{ Scan(...any) error }

func scanHydratedHandoff(row handoffRowScanner) (HydratedChatHandoff, error) {
	var result HydratedChatHandoff
	var hasTask bool
	var task model.ScheduledTask
	var tools, monitoring []byte
	err := row.Scan(
		&result.Handoff.ID, &result.Handoff.UserID, &result.Handoff.SourceConversationID, &result.Handoff.SourceUserMessageID,
		&result.Handoff.SourceContentFingerprint, &result.Handoff.AssistantMessageID, &result.Handoff.ScheduledTaskID,
		&result.Handoff.InvocationOrdinal, &result.Handoff.ArtifactState, &result.Handoff.ErrorCode, &result.Handoff.Retryable,
		&result.Handoff.CreatedAt, &result.Handoff.UpdatedAt,
		&hasTask, &task.ID, &task.UserID, &task.ConversationID, &task.Version, &task.Name, &task.Kind, &task.State,
		&task.CompiledPrompt, &task.OneOffAt, &task.DTStart, &task.RRULE, &task.Timezone, &task.ExecutionMode, &tools,
		&monitoring, &task.DeliveryPolicy, &task.InitialRun, &task.StopCondition, &task.StaticMessage,
		&task.ConsecutiveFailures, &task.NextRunAt, &task.LastRunAt, &task.CreatedAt, &task.UpdatedAt, &task.DeletedAt,
		&result.LatestDefinitionAssistant,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return HydratedChatHandoff{}, ErrNotFound
	}
	if err != nil {
		return HydratedChatHandoff{}, fmt.Errorf("scan chat handoff: %w", err)
	}
	if hasTask {
		if err := json.Unmarshal(tools, &task.AuthorizedTools); err != nil {
			return HydratedChatHandoff{}, fmt.Errorf("decode chat handoff task tools: %w", err)
		}
		task.MonitoringState = append(task.MonitoringState[:0], monitoring...)
		result.Task = &task
	}
	return result, nil
}

func getHandoffByID(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID int64, handoffID string) (HydratedChatHandoff, error) {
	return scanHydratedHandoff(db.QueryRow(ctx, handoffHydrationQuery+` WHERE h.user_id = $1 AND h.id = $2::uuid`, userID, handoffID))
}

func getHandoffBySlot(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, in CreateChatHandoffInput) (HydratedChatHandoff, error) {
	return scanHydratedHandoff(db.QueryRow(ctx, handoffHydrationQuery+`
	 WHERE h.user_id = $1 AND h.source_user_message_id = $2 AND h.source_content_fingerprint = $3 AND h.invocation_ordinal = $4`,
		in.UserID, in.SourceUserMessageID, in.SourceContentFingerprint, in.InvocationOrdinal))
}

func cleanupDraftHandoffs(ctx context.Context, tx pgx.Tx, userID int64, handoffIDs []string) error {
	rows, err := tx.Query(ctx,
		`SELECT h.id::text, h.scheduled_task_id::text, task.conversation_id::text
		   FROM chat_scheduled_handoffs AS h
		   JOIN scheduled_tasks AS task ON task.id = h.scheduled_task_id
		  WHERE h.user_id = $1 AND h.id = ANY($2::uuid[]) AND task.user_id = $1 AND task.state = $3
		  FOR UPDATE OF h, task`,
		userID, handoffIDs, model.ScheduledTaskStateDraft)
	if err != nil {
		return fmt.Errorf("select chat handoff drafts for cleanup: %w", err)
	}
	defer rows.Close()
	type draft struct{ handoffID, taskID, conversationID string }
	var drafts []draft
	for rows.Next() {
		var found draft
		if err := rows.Scan(&found.handoffID, &found.taskID, &found.conversationID); err != nil {
			return fmt.Errorf("scan chat handoff draft cleanup: %w", err)
		}
		drafts = append(drafts, found)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, draft := range drafts {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_scheduled_handoffs WHERE id = $1::uuid AND user_id = $2`, draft.handoffID, userID); err != nil {
			return fmt.Errorf("delete cleaned chat handoff: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM scheduled_tasks WHERE id = $1::uuid AND user_id = $2 AND state = $3`, draft.taskID, userID, model.ScheduledTaskStateDraft); err != nil {
			return fmt.Errorf("delete chat handoff draft task: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM conversations WHERE id = $1::uuid AND user_id = $2 AND kind = $3`, draft.conversationID, userID, model.ConversationKindScheduled); err != nil {
			return fmt.Errorf("delete chat handoff scheduled conversation: %w", err)
		}
	}
	return nil
}

func cleanupDraftHandoffsForSourceMessages(
	ctx context.Context, tx pgx.Tx, userID int64, conversationID string, fromMessageID int64,
) error {
	rows, err := tx.Query(ctx,
		`SELECT id::text FROM chat_scheduled_handoffs
		  WHERE user_id = $1 AND source_conversation_id = $2::uuid AND source_user_message_id >= $3`,
		userID, conversationID, fromMessageID)
	if err != nil {
		return fmt.Errorf("select chat handoffs for source-message rewind: %w", err)
	}
	defer rows.Close()
	var handoffIDs []string
	for rows.Next() {
		var handoffID string
		if err := rows.Scan(&handoffID); err != nil {
			return fmt.Errorf("scan chat handoff source-message rewind: %w", err)
		}
		handoffIDs = append(handoffIDs, handoffID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return cleanupDraftHandoffs(ctx, tx, userID, handoffIDs)
}

func cleanupDraftHandoffsForAssistantMessages(
	ctx context.Context, tx pgx.Tx, userID int64, conversationID string, fromMessageID int64,
) error {
	rows, err := tx.Query(ctx,
		`SELECT id::text FROM chat_scheduled_handoffs
		  WHERE user_id = $1 AND source_conversation_id = $2::uuid AND assistant_message_id >= $3`,
		userID, conversationID, fromMessageID)
	if err != nil {
		return fmt.Errorf("select chat handoffs for assistant rewind: %w", err)
	}
	defer rows.Close()
	var handoffIDs []string
	for rows.Next() {
		var handoffID string
		if err := rows.Scan(&handoffID); err != nil {
			return fmt.Errorf("scan chat handoff assistant rewind: %w", err)
		}
		handoffIDs = append(handoffIDs, handoffID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return cleanupDraftHandoffs(ctx, tx, userID, handoffIDs)
}

func cleanupDraftHandoffsForConversation(ctx context.Context, tx pgx.Tx, userID int64, conversationID string) error {
	rows, err := tx.Query(ctx,
		`SELECT id::text FROM chat_scheduled_handoffs WHERE user_id = $1 AND source_conversation_id = $2::uuid`,
		userID, conversationID)
	if err != nil {
		return fmt.Errorf("select chat handoffs for conversation cleanup: %w", err)
	}
	defer rows.Close()
	var handoffIDs []string
	for rows.Next() {
		var handoffID string
		if err := rows.Scan(&handoffID); err != nil {
			return fmt.Errorf("scan chat handoff conversation cleanup: %w", err)
		}
		handoffIDs = append(handoffIDs, handoffID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return cleanupDraftHandoffs(ctx, tx, userID, handoffIDs)
}

func discardDraftTask(ctx context.Context, tx pgx.Tx, userID int64, taskID string, retainTombstone bool) error {
	var handoffID, conversationID, taskState string
	err := tx.QueryRow(ctx,
		`SELECT h.id::text, task.conversation_id::text, task.state
		   FROM chat_scheduled_handoffs AS h
		   JOIN scheduled_tasks AS task ON task.id = h.scheduled_task_id
		  WHERE h.user_id = $1 AND h.scheduled_task_id = $2::uuid AND task.user_id = $1
		  FOR UPDATE OF h, task`,
		userID, taskID).Scan(&handoffID, &conversationID, &taskState)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load chat handoff draft: %w", err)
	}
	if taskState != model.ScheduledTaskStateDraft {
		return ErrInvalidScheduledTaskState
	}
	if retainTombstone {
		if _, err := tx.Exec(ctx,
			`UPDATE chat_scheduled_handoffs SET artifact_state = $1, scheduled_task_id = NULL, error_code = '', retryable = FALSE, updated_at = NOW()
			 WHERE id = $2::uuid AND user_id = $3`, model.ScheduledHandoffStateDismissed, handoffID, userID); err != nil {
			return fmt.Errorf("retain dismissed chat handoff: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `DELETE FROM chat_scheduled_handoffs WHERE id = $1::uuid AND user_id = $2`, handoffID, userID); err != nil {
		return fmt.Errorf("delete chat handoff: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM scheduled_tasks WHERE id = $1::uuid AND user_id = $2 AND state = $3`, taskID, userID, model.ScheduledTaskStateDraft); err != nil {
		return fmt.Errorf("delete chat handoff draft task: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM conversations WHERE id = $1::uuid AND user_id = $2 AND kind = $3`, conversationID, userID, model.ConversationKindScheduled); err != nil {
		return fmt.Errorf("delete chat handoff scheduled conversation: %w", err)
	}
	return nil
}
