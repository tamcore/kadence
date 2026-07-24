package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// MessageRepository accesses the messages table.
type MessageRepository struct{ pool *pgxpool.Pool }

const (
	messagePurposeChat                = "chat"
	messagePurposeScheduledDefinition = "scheduled_definition"
)

// NewMessageRepository returns a MessageRepository.
func NewMessageRepository(pool *pgxpool.Pool) *MessageRepository {
	return &MessageRepository{pool: pool}
}

// Add appends a message to a conversation (no tool-call audit record).
func (r *MessageRepository) Add(ctx context.Context, conversationID string, role, content string) (model.Message, error) {
	return r.AddWithToolCalls(ctx, conversationID, role, content, nil)
}

// AddWithToolCalls appends a message, recording the tool calls the assistant
// made while producing it (nil/empty stores SQL NULL).
func (r *MessageRepository) AddWithToolCalls(ctx context.Context, conversationID string, role, content string, toolCalls []model.MessageToolCall) (model.Message, error) {
	return r.addWithPurpose(ctx, conversationID, role, content, toolCalls, messagePurposeChat)
}

// AddDefinition appends one user or assistant exchange to a Scheduled task's
// definition thread. Execution deliveries use a separate purpose so they can
// never consume or evict definition-model context.
func (r *MessageRepository) AddDefinition(ctx context.Context, conversationID string, role, content string) (model.Message, error) {
	return r.addWithPurpose(ctx, conversationID, role, content, nil, messagePurposeScheduledDefinition)
}

func (r *MessageRepository) addWithPurpose(ctx context.Context, conversationID string, role, content string, toolCalls []model.MessageToolCall, purpose string) (model.Message, error) {
	var raw []byte
	if len(toolCalls) > 0 {
		b, err := json.Marshal(toolCalls)
		if err != nil {
			return model.Message{}, fmt.Errorf("marshal tool_calls: %w", err)
		}
		raw = b
	}
	var m model.Message
	var tcRaw []byte
	err := r.pool.QueryRow(ctx,
		`INSERT INTO messages (conversation_id, role, content, tool_calls, purpose) VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING id, conversation_id::text, role, content, tool_calls, created_at`, conversationID, role, content, raw, purpose).
		Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &tcRaw, &m.CreatedAt)
	if err != nil {
		return model.Message{}, fmt.Errorf("insert message: %w", err)
	}
	if len(tcRaw) > 0 {
		if err := json.Unmarshal(tcRaw, &m.ToolCalls); err != nil {
			return model.Message{}, fmt.Errorf("scan tool_calls: %w", err)
		}
	}
	return m, nil
}

// ListByConversation returns a conversation's messages in chronological order.
func (r *MessageRepository) ListByConversation(ctx context.Context, conversationID string) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id::text, role, content, tool_calls, created_at FROM messages
		 WHERE conversation_id = $1::uuid ORDER BY id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return scanMessages(rows)
}

// ListRecentByConversation returns at most limit newest messages while
// preserving chronological order in the returned conversation history.
func (r *MessageRepository) ListRecentByConversation(ctx context.Context, conversationID string, limit int) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, created_at
		   FROM (
		        SELECT id, conversation_id::text, role, content, tool_calls, created_at
		          FROM messages
		         WHERE conversation_id = $1::uuid
		         ORDER BY id DESC
		         LIMIT $2
		   ) AS recent
		  ORDER BY id`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent messages: %w", err)
	}
	return scanMessages(rows)
}

// ListRecentDefinitionByConversation returns only Scheduled definition
// exchanges. Unattended delivery messages remain visible in the conversation
// and run history but cannot consume the definition compiler's bounded context.
func (r *MessageRepository) ListRecentDefinitionByConversation(ctx context.Context, conversationID string, limit int) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, created_at
		   FROM (
		        SELECT id, conversation_id::text, role, content, tool_calls, created_at
		          FROM messages
		         WHERE conversation_id = $1::uuid AND purpose = $2
		         ORDER BY id DESC
		         LIMIT $3
		   ) AS recent
		  ORDER BY id`,
		conversationID, messagePurposeScheduledDefinition, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent scheduled definition messages: %w", err)
	}
	return scanMessages(rows)
}

func scanMessages(rows pgx.Rows) ([]model.Message, error) {
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var m model.Message
		var tcRaw []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &tcRaw, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if len(tcRaw) > 0 {
			if err := json.Unmarshal(tcRaw, &m.ToolCalls); err != nil {
				return nil, fmt.Errorf("scan tool_calls: %w", err)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
