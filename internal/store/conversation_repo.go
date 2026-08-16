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

// conversationCols is the column list every conversation query selects, in the
// order scanConversation reads them. Keep the two in step.
const conversationCols = "id::text, user_id, title, kind, pinned_at, last_activity_at, created_at"

func scanConversation(row rowScanner) (model.Conversation, error) {
	var c model.Conversation
	err := row.Scan(&c.ID, &c.UserID, &c.Title, &c.Kind, &c.PinnedAt,
		&c.LastActivityAt, &c.CreatedAt)
	return c, err
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
	c, err := scanConversation(db.QueryRow(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, $2, $3)
		 RETURNING `+conversationCols, userID, title, kind))
	if err != nil {
		return model.Conversation{}, fmt.Errorf("insert conversation: %w", err)
	}
	return c, nil
}

// GetByID returns a conversation owned by userID, or ErrNotFound.
func (r *ConversationRepository) GetByID(ctx context.Context, id string, userID int64) (model.Conversation, error) {
	c, err := scanConversation(r.pool.QueryRow(ctx,
		`SELECT `+conversationCols+` FROM conversations WHERE id = $1::uuid AND user_id = $2`,
		id, userID))
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
		`SELECT `+conversationCols+`
		   FROM conversations
		  WHERE user_id = $1 AND kind = $2
		  ORDER BY pinned_at DESC NULLS LAST, last_activity_at DESC, created_at DESC, id DESC`,
		userID, model.ConversationKindChat)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Conversation, error) {
		return scanConversation(row)
	})
	if err != nil {
		return nil, fmt.Errorf("scan conversation: %w", err)
	}
	return out, nil
}

// UpdateTitle sets the title of a conversation owned by userID, or returns
// ErrNotFound if no row matched (wrong id or not the owner).
func (r *ConversationRepository) UpdateTitle(ctx context.Context, id string, userID int64, title string) (model.Conversation, error) {
	c, err := scanConversation(r.pool.QueryRow(ctx,
		`UPDATE conversations SET title = $1 WHERE id = $2::uuid AND user_id = $3
		 RETURNING `+conversationCols, title, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Conversation{}, ErrNotFound
	}
	if err != nil {
		return model.Conversation{}, fmt.Errorf("update conversation title: %w", err)
	}
	return c, nil
}

// UpdateTitleIfCurrent sets a title only when the conversation belongs to the
// user and still has currentTitle.
func (r *ConversationRepository) UpdateTitleIfCurrent(
	ctx context.Context,
	id string,
	userID int64,
	currentTitle string,
	newTitle string,
) (model.Conversation, bool, error) {
	c, err := scanConversation(r.pool.QueryRow(ctx,
		`UPDATE conversations
		    SET title = $1
		  WHERE id = $2::uuid AND user_id = $3 AND title = $4
		  RETURNING `+conversationCols,
		newTitle, id, userID, currentTitle))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Conversation{}, false, nil
	}
	if err != nil {
		return model.Conversation{}, false, fmt.Errorf("conditionally update conversation title: %w", err)
	}
	return c, true, nil
}

// UpdatePinned changes the pin state of an owned ordinary chat conversation.
// Re-pinning preserves the original pin timestamp and repeated unpins remain
// no-ops, making both operations idempotent.
func (r *ConversationRepository) UpdatePinned(ctx context.Context, id string, userID int64, pinned bool) (model.Conversation, error) {
	c, err := scanConversation(r.pool.QueryRow(ctx,
		`UPDATE conversations
		    SET pinned_at = CASE WHEN $1 THEN COALESCE(pinned_at, NOW()) ELSE NULL END
		  WHERE id = $2::uuid AND user_id = $3 AND kind = $4
		  RETURNING `+conversationCols,
		pinned, id, userID, model.ConversationKindChat))
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
	return inTxErr(ctx, r.pool, "delete conversation", func(tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM conversations WHERE id = $1::uuid AND user_id = $2 FOR UPDATE`, id, userID).Scan(&kind)
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
		return nil
	})
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
