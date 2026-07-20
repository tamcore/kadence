package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// ConversationRepository accesses the conversations table.
type ConversationRepository struct{ pool *pgxpool.Pool }

// NewConversationRepository returns a ConversationRepository.
func NewConversationRepository(pool *pgxpool.Pool) *ConversationRepository {
	return &ConversationRepository{pool: pool}
}

// Create inserts a new conversation for a user.
func (r *ConversationRepository) Create(ctx context.Context, userID int64, title string) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx,
		`INSERT INTO conversations (user_id, title) VALUES ($1, $2)
		 RETURNING id::text, user_id, title, created_at`, userID, title).
		Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt)
	if err != nil {
		return model.Conversation{}, fmt.Errorf("insert conversation: %w", err)
	}
	return c, nil
}

// GetByID returns a conversation owned by userID, or ErrNotFound.
func (r *ConversationRepository) GetByID(ctx context.Context, id string, userID int64) (model.Conversation, error) {
	var c model.Conversation
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, user_id, title, created_at FROM conversations WHERE id = $1::uuid AND user_id = $2`, id, userID).
		Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Conversation{}, ErrNotFound
	}
	if err != nil {
		return model.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

// ListByUser returns a user's conversations, newest first.
func (r *ConversationRepository) ListByUser(ctx context.Context, userID int64) ([]model.Conversation, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, user_id, title, created_at FROM conversations WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var out []model.Conversation
	for rows.Next() {
		var c model.Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a conversation owned by userID (cascades to messages).
func (r *ConversationRepository) Delete(ctx context.Context, id string, userID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM conversations WHERE id = $1::uuid AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}
