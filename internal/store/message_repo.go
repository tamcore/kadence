package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// MessageRepository accesses the messages table.
type MessageRepository struct{ pool *pgxpool.Pool }

// NewMessageRepository returns a MessageRepository.
func NewMessageRepository(pool *pgxpool.Pool) *MessageRepository {
	return &MessageRepository{pool: pool}
}

// Add appends a message to a conversation.
func (r *MessageRepository) Add(ctx context.Context, conversationID string, role, content string) (model.Message, error) {
	var m model.Message
	err := r.pool.QueryRow(ctx,
		`INSERT INTO messages (conversation_id, role, content) VALUES ($1::uuid, $2, $3)
		 RETURNING id, conversation_id::text, role, content, created_at`, conversationID, role, content).
		Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt)
	if err != nil {
		return model.Message{}, fmt.Errorf("insert message: %w", err)
	}
	return m, nil
}

// ListByConversation returns a conversation's messages in chronological order.
func (r *MessageRepository) ListByConversation(ctx context.Context, conversationID string) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id::text, role, content, created_at FROM messages
		 WHERE conversation_id = $1::uuid ORDER BY id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
