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

// MessageRepository accesses the messages table.
type MessageRepository struct{ pool *pgxpool.Pool }

// ErrWrongMessageRole reports that a rewind target exists but has the wrong
// role for the requested operation.
var ErrWrongMessageRole = errors.New("wrong message role")

// ErrStaleChatTurn means the user message a streamed assistant response was
// generated for is no longer the latest ordinary chat message.
var ErrStaleChatTurn = errors.New("store: chat turn is stale")

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

// AddChatUser appends an ordinary chat user message while holding the
// conversation row lock. Assistant writes use the same lock, so a later user
// turn or rewind is ordered before a stale assistant can be appended.
func (r *MessageRepository) AddChatUser(ctx context.Context, conversationID, content string) (model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Message{}, fmt.Errorf("begin add chat user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockChatConversation(ctx, tx, conversationID); err != nil {
		return model.Message{}, err
	}
	message, err := addMessageWithPurpose(ctx, tx, conversationID, model.MsgRoleUser, content, nil, messagePurposeChat)
	if err != nil {
		return model.Message{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit add chat user: %w", err)
	}
	return message, nil
}

// AddChatAssistantIfLatestUser appends an ordinary chat assistant message
// only when expectedUser is still the latest ordinary chat user message. The
// conversation row lock serializes this CAS against new turns and rewinds.
func (r *MessageRepository) AddChatAssistantIfLatestUser(
	ctx context.Context, conversationID string, expectedUser model.Message, content string, toolCalls []model.MessageToolCall, handoffIDs []string,
) (model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Message{}, fmt.Errorf("begin add chat assistant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockChatConversation(ctx, tx, conversationID); err != nil {
		return model.Message{}, err
	}
	var latestID int64
	var latestRole, latestContent string
	err = tx.QueryRow(ctx,
		`SELECT id, role, content FROM messages
		  WHERE conversation_id = $1::uuid AND purpose = $2
		  ORDER BY id DESC LIMIT 1`,
		conversationID, messagePurposeChat,
	).Scan(&latestID, &latestRole, &latestContent)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Message{}, ErrStaleChatTurn
	}
	if err != nil {
		return model.Message{}, fmt.Errorf("load latest chat message: %w", err)
	}
	if latestID != expectedUser.ID || latestRole != model.MsgRoleUser || latestContent != expectedUser.Content {
		return model.Message{}, ErrStaleChatTurn
	}
	message, err := addMessageWithPurpose(ctx, tx, conversationID, model.MsgRoleAssistant, content, toolCalls, messagePurposeChat)
	if err != nil {
		return model.Message{}, err
	}
	if len(handoffIDs) > 0 {
		distinct := make(map[string]struct{}, len(handoffIDs))
		for _, handoffID := range handoffIDs {
			distinct[handoffID] = struct{}{}
		}
		command, err := tx.Exec(ctx,
			`UPDATE chat_scheduled_handoffs
			   SET assistant_message_id = $1, updated_at = NOW()
			 WHERE id = ANY($2::uuid[])
			   AND source_conversation_id = $3::uuid`,
			message.ID, handoffIDs, conversationID)
		if err != nil {
			return model.Message{}, fmt.Errorf("bind chat scheduling handoffs: %w", err)
		}
		if command.RowsAffected() != int64(len(distinct)) {
			return model.Message{}, ErrNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit add chat assistant: %w", err)
	}
	return message, nil
}

// AddDefinition appends one user or assistant exchange to a Scheduled task's
// definition thread. Execution deliveries use a separate purpose so they can
// never consume or evict definition-model context.
func (r *MessageRepository) AddDefinition(ctx context.Context, conversationID string, role, content string) (model.Message, error) {
	return r.addWithPurpose(ctx, conversationID, role, content, nil, messagePurposeScheduledDefinition)
}

func (r *MessageRepository) addWithPurpose(ctx context.Context, conversationID string, role, content string, toolCalls []model.MessageToolCall, purpose string) (model.Message, error) {
	return addMessageWithPurpose(ctx, r.pool, conversationID, role, content, toolCalls, purpose)
}

type messageRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func addMessageWithPurpose(
	ctx context.Context, db messageRowQuerier, conversationID string, role, content string,
	toolCalls []model.MessageToolCall, purpose string,
) (model.Message, error) {
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
	err := db.QueryRow(ctx,
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

// GetByID returns one message scoped to its conversation.
func (r *MessageRepository) GetByID(
	ctx context.Context, conversationID string, messageID int64,
) (model.Message, error) {
	message, err := scanMessageRow(r.pool.QueryRow(ctx,
		`SELECT id, conversation_id::text, role, content, tool_calls, created_at
		   FROM messages WHERE conversation_id = $1::uuid AND id = $2`,
		conversationID, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Message{}, ErrNotFound
	}
	if err != nil {
		return model.Message{}, fmt.Errorf("get message: %w", err)
	}
	return message, nil
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

// EditAndRewind updates one owned ordinary-chat user message and removes every
// later message plus every RAG chunk derived from the rewritten suffix.
func (r *MessageRepository) EditAndRewind(
	ctx context.Context, conversationID string, messageID, userID int64, content string,
) (model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Message{}, fmt.Errorf("begin edit rewind: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedChat(ctx, tx, conversationID, userID); err != nil {
		return model.Message{}, err
	}
	target, err := getMessageForRewind(ctx, tx, conversationID, messageID)
	if err != nil {
		return model.Message{}, err
	}
	if target.Role != model.MsgRoleUser {
		return model.Message{}, ErrWrongMessageRole
	}
	if err := cleanupDraftHandoffsForSourceMessages(ctx, tx, userID, conversationID, messageID); err != nil {
		return model.Message{}, err
	}
	if err := deleteMessageChunksFrom(ctx, tx, conversationID, messageID); err != nil {
		return model.Message{}, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM messages WHERE conversation_id = $1::uuid AND id > $2`,
		conversationID, messageID); err != nil {
		return model.Message{}, fmt.Errorf("delete messages after edit target: %w", err)
	}
	target.Content = content
	if _, err := tx.Exec(ctx,
		`UPDATE messages SET content = $3 WHERE conversation_id = $1::uuid AND id = $2`,
		conversationID, messageID, content); err != nil {
		return model.Message{}, fmt.Errorf("update edited message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit edit rewind: %w", err)
	}
	return target, nil
}

// RegenerateAndRewind removes one owned ordinary-chat assistant message and
// every later message plus their derived RAG chunks, returning its preceding
// user prompt for regeneration.
func (r *MessageRepository) RegenerateAndRewind(
	ctx context.Context, conversationID string, messageID, userID int64,
) (model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Message{}, fmt.Errorf("begin regenerate rewind: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedChat(ctx, tx, conversationID, userID); err != nil {
		return model.Message{}, err
	}
	target, err := getMessageForRewind(ctx, tx, conversationID, messageID)
	if err != nil {
		return model.Message{}, err
	}
	if target.Role != model.MsgRoleAssistant {
		return model.Message{}, ErrWrongMessageRole
	}
	if err := cleanupDraftHandoffsForAssistantMessages(ctx, tx, userID, conversationID, messageID); err != nil {
		return model.Message{}, err
	}
	prompt, err := scanMessageRow(tx.QueryRow(ctx,
		`SELECT id, conversation_id::text, role, content, tool_calls, created_at
		   FROM messages
		  WHERE conversation_id = $1::uuid AND id < $2 AND role = $3
		  ORDER BY id DESC LIMIT 1`,
		conversationID, messageID, model.MsgRoleUser))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Message{}, ErrNotFound
	}
	if err != nil {
		return model.Message{}, fmt.Errorf("load regenerate prompt: %w", err)
	}
	if err := deleteMessageChunksFrom(ctx, tx, conversationID, messageID); err != nil {
		return model.Message{}, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM messages WHERE conversation_id = $1::uuid AND id >= $2`,
		conversationID, messageID); err != nil {
		return model.Message{}, fmt.Errorf("delete regenerated message suffix: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit regenerate rewind: %w", err)
	}
	return prompt, nil
}

func lockOwnedChat(ctx context.Context, tx pgx.Tx, conversationID string, userID int64) error {
	var id string
	err := tx.QueryRow(ctx,
		`SELECT id::text FROM conversations
		  WHERE id = $1::uuid AND user_id = $2 AND kind = $3
		  FOR UPDATE`,
		conversationID, userID, model.ConversationKindChat).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock chat conversation: %w", err)
	}
	return nil
}

func lockChatConversation(ctx context.Context, tx pgx.Tx, conversationID string) error {
	var id string
	err := tx.QueryRow(ctx,
		`SELECT id::text FROM conversations
		  WHERE id = $1::uuid AND kind = $2
		  FOR UPDATE`,
		conversationID, model.ConversationKindChat).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock chat conversation: %w", err)
	}
	return nil
}

func getMessageForRewind(
	ctx context.Context, tx pgx.Tx, conversationID string, messageID int64,
) (model.Message, error) {
	message, err := scanMessageRow(tx.QueryRow(ctx,
		`SELECT id, conversation_id::text, role, content, tool_calls, created_at
		   FROM messages WHERE conversation_id = $1::uuid AND id = $2`,
		conversationID, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Message{}, ErrNotFound
	}
	if err != nil {
		return model.Message{}, fmt.Errorf("load rewind message: %w", err)
	}
	return message, nil
}

func deleteMessageChunksFrom(
	ctx context.Context, tx pgx.Tx, conversationID string, messageID int64,
) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM chunks
		  WHERE conversation_id = $1::uuid
		    AND source_kind = $3
		    AND source_id IN (
		        SELECT id FROM messages
		         WHERE conversation_id = $1::uuid AND id >= $2
		    )`,
		conversationID, messageID, model.ChunkSourceMessage)
	if err != nil {
		return fmt.Errorf("delete rewound message chunks: %w", err)
	}
	return nil
}

func scanMessageRow(row pgx.Row) (model.Message, error) {
	var message model.Message
	var toolCallsRaw []byte
	err := row.Scan(
		&message.ID, &message.ConversationID, &message.Role, &message.Content,
		&toolCallsRaw, &message.CreatedAt,
	)
	if err != nil {
		return model.Message{}, err
	}
	if len(toolCallsRaw) > 0 {
		if err := json.Unmarshal(toolCallsRaw, &message.ToolCalls); err != nil {
			return model.Message{}, fmt.Errorf("scan tool_calls: %w", err)
		}
	}
	return message, nil
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
