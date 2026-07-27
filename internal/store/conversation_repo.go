package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// ErrConversationHasActiveDelivery is returned by Delete when the conversation
// is the active delivery_conversation_id of a scheduled task (ON DELETE
// RESTRICT). Handlers should map this to a 409, not a generic 500.
var ErrConversationHasActiveDelivery = errors.New("store: conversation has an active scheduled delivery")

// ConversationRepository accesses the conversations table.
type ConversationRepository struct{ pool *pgxpool.Pool }

// NewConversationRepository returns a ConversationRepository.
func NewConversationRepository(pool *pgxpool.Pool) *ConversationRepository {
	return &ConversationRepository{pool: pool}
}

// Create inserts a new conversation for a user.
func (r *ConversationRepository) Create(ctx context.Context, userID int64, title string) (model.Conversation, error) {
	return r.CreateWithKind(ctx, userID, title, model.ConversationKindChat)
}

// CreateWithKind inserts a conversation of the requested kind for a user.
func (r *ConversationRepository) CreateWithKind(ctx context.Context, userID int64, title, kind string) (model.Conversation, error) {
	return insertConversation(ctx, r.pool, userID, title, kind)
}

func insertConversation(
	ctx context.Context, db messageRowQuerier, userID int64, title, kind string,
) (model.Conversation, error) {
	var c model.Conversation
	err := db.QueryRow(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, $2, $3)
		 RETURNING id::text, user_id, title, kind, pinned_at, last_activity_at, created_at`, userID, title, kind).
		Scan(&c.ID, &c.UserID, &c.Title, &c.Kind, &c.PinnedAt, &c.LastActivityAt, &c.CreatedAt)
	if err != nil {
		return model.Conversation{}, fmt.Errorf("insert conversation: %w", err)
	}
	return c, nil
}

// GetByID returns a conversation owned by userID, or ErrNotFound.
func (r *ConversationRepository) GetByID(ctx context.Context, id string, userID int64) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, user_id, title, kind, pinned_at, last_activity_at, created_at FROM conversations WHERE id = $1::uuid AND user_id = $2`, id, userID).
		Scan(&c.ID, &c.UserID, &c.Title, &c.Kind, &c.PinnedAt, &c.LastActivityAt, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Conversation{}, ErrNotFound
	}
	if err != nil {
		return model.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

// ListByUser returns a user's chat conversations with pinned chats first,
// followed by most recently active chats.
func (r *ConversationRepository) ListByUser(ctx context.Context, userID int64) ([]model.Conversation, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, user_id, title, kind, pinned_at, last_activity_at, created_at
		   FROM conversations
		  WHERE user_id = $1 AND kind = $2
		  ORDER BY pinned_at DESC NULLS LAST, last_activity_at DESC, created_at DESC, id DESC`,
		userID, model.ConversationKindChat)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var out []model.Conversation
	for rows.Next() {
		var c model.Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.Kind, &c.PinnedAt, &c.LastActivityAt, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateTitle sets the title of a conversation owned by userID, or returns
// ErrNotFound if no row matched (wrong id or not the owner).
func (r *ConversationRepository) UpdateTitle(ctx context.Context, id string, userID int64, title string) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx,
		`UPDATE conversations SET title = $1 WHERE id = $2::uuid AND user_id = $3
		 RETURNING id::text, user_id, title, kind, pinned_at, last_activity_at, created_at`, title, id, userID).
		Scan(&c.ID, &c.UserID, &c.Title, &c.Kind, &c.PinnedAt, &c.LastActivityAt, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Conversation{}, ErrNotFound
	}
	if err != nil {
		return model.Conversation{}, fmt.Errorf("update conversation title: %w", err)
	}
	return c, nil
}

// UpdatePinned changes the pin state of an owned ordinary chat conversation.
// Re-pinning preserves the original pin timestamp and repeated unpins remain
// no-ops, making both operations idempotent.
func (r *ConversationRepository) UpdatePinned(ctx context.Context, id string, userID int64, pinned bool) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx,
		`UPDATE conversations
		    SET pinned_at = CASE WHEN $1 THEN COALESCE(pinned_at, NOW()) ELSE NULL END
		  WHERE id = $2::uuid AND user_id = $3 AND kind = $4
		  RETURNING id::text, user_id, title, kind, pinned_at, last_activity_at, created_at`,
		pinned, id, userID, model.ConversationKindChat,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.Kind, &c.PinnedAt, &c.LastActivityAt, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Conversation{}, ErrNotFound
	}
	if err != nil {
		return model.Conversation{}, fmt.Errorf("update conversation pin: %w", err)
	}
	return c, nil
}

// Delete removes a conversation owned by userID (cascades to messages). Before
// an ordinary source chat is removed, any still-draft handoff task and its
// Scheduled definition conversation are hard-cleaned; confirmed work remains.
func (r *ConversationRepository) Delete(ctx context.Context, id string, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete conversation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM conversations WHERE id = $1::uuid AND user_id = $2 FOR UPDATE`, id, userID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock delete conversation: %w", err)
	}
	if kind == model.ConversationKindChat {
		if err := cleanupDraftHandoffsForConversation(ctx, tx, userID, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM conversations WHERE id = $1::uuid AND user_id = $2`, id, userID); err != nil {
		if isDeliveryConversationForeignKeyViolation(err) {
			return ErrConversationHasActiveDelivery
		}
		return fmt.Errorf("delete conversation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete conversation: %w", err)
	}
	return nil
}

// isDeliveryConversationForeignKeyViolation reports whether err is a Postgres
// foreign-key violation (23503) caused by scheduled_tasks.delivery_conversation_id
// still referencing this conversation (ON DELETE RESTRICT).
func isDeliveryConversationForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23503" &&
		pgErr.ConstraintName == "scheduled_tasks_delivery_conversation_id_fkey"
}
