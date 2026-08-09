package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// MessageRepository accesses the messages table.
type MessageRepository struct{ pool *pgxpool.Pool }

// ChatUserInput remains an alias for callers that used the store package
// before the shared input contract moved to model.
type ChatUserInput = model.ChatUserInput

// ErrWrongMessageRole reports that a rewind target exists but has the wrong
// role for the requested operation.
var ErrWrongMessageRole = errors.New("wrong message role")

// ErrStaleChatTurn means the user message a streamed assistant response was
// generated for is no longer the latest ordinary chat message.
var ErrStaleChatTurn = errors.New("store: chat turn is stale")

const (
	messagePurposeChat                = model.MessagePurposeChat
	messagePurposeScheduledDefinition = model.MessagePurposeScheduledDefinition
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

// AddChatUserInput atomically appends one ordinary chat user message and its
// attachments/document references while holding the owned conversation lock.
func (r *MessageRepository) AddChatUserInput(
	ctx context.Context, conversationID string, userID int64, input model.ChatUserInput,
) (model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Message{}, fmt.Errorf("begin add chat user input: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedChat(ctx, tx, conversationID, userID); err != nil {
		return model.Message{}, err
	}
	message, err := addMessageWithPurpose(
		ctx, tx, conversationID, model.MsgRoleUser, input.Content, nil, messagePurposeChat,
	)
	if err != nil {
		return model.Message{}, err
	}
	message.Attachments, err = insertMessageAttachments(ctx, tx, message.ID, input.Attachments)
	if err != nil {
		return model.Message{}, err
	}
	message.DocumentReferences, err = insertMessageDocumentReferences(
		ctx, tx, message.ID, userID, input.DocumentIDs,
	)
	if err != nil {
		return model.Message{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit add chat user input: %w", err)
	}
	return message, nil
}

// CreateConversationWithChatUserInput atomically creates one ordinary chat
// conversation and its first rich user input. A failure while inserting the
// message, attachments, or document references rolls the conversation back.
func (r *MessageRepository) CreateConversationWithChatUserInput(
	ctx context.Context, userID int64, title string, input model.ChatUserInput,
) (model.Conversation, model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Conversation{}, model.Message{},
			fmt.Errorf("begin create conversation with chat user input: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	conversation, err := insertConversation(
		ctx, tx, userID, title, model.ConversationKindChat,
	)
	if err != nil {
		return model.Conversation{}, model.Message{}, err
	}
	message, err := addMessageWithPurpose(
		ctx, tx, conversation.ID, model.MsgRoleUser, input.Content, nil, messagePurposeChat,
	)
	if err != nil {
		return model.Conversation{}, model.Message{}, err
	}
	message.Attachments, err = insertMessageAttachments(ctx, tx, message.ID, input.Attachments)
	if err != nil {
		return model.Conversation{}, model.Message{}, err
	}
	message.DocumentReferences, err = insertMessageDocumentReferences(
		ctx, tx, message.ID, userID, input.DocumentIDs,
	)
	if err != nil {
		return model.Conversation{}, model.Message{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Conversation{}, model.Message{},
			fmt.Errorf("commit create conversation with chat user input: %w", err)
	}
	return conversation, message, nil
}

// UpdateChatAttachmentExtractions atomically persists deferred document
// extraction results for one owned ordinary-chat user message.
func (r *MessageRepository) UpdateChatAttachmentExtractions(
	ctx context.Context, conversationID string, messageID, userID int64,
	attachments []model.MessageAttachment,
) (model.Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Message{}, fmt.Errorf("begin update chat attachment extractions: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedChat(ctx, tx, conversationID, userID); err != nil {
		return model.Message{}, err
	}
	message, err := getMessageForRewind(ctx, tx, conversationID, messageID)
	if err != nil {
		return model.Message{}, err
	}
	if message.Role != model.MsgRoleUser {
		return model.Message{}, ErrWrongMessageRole
	}
	messages := []model.Message{message}
	if err := hydrateMessageRelations(ctx, tx, messages, true); err != nil {
		return model.Message{}, err
	}
	message = messages[0]
	if len(message.Attachments) != len(attachments) {
		return model.Message{}, ErrNotFound
	}
	for i := range attachments {
		stored := &message.Attachments[i]
		extracted := attachments[i]
		if stored.ID != extracted.ID ||
			stored.MessageID != extracted.MessageID ||
			stored.Ordinal != extracted.Ordinal ||
			stored.Kind != extracted.Kind {
			return model.Message{}, ErrNotFound
		}
		if stored.Kind != model.AttachmentKindDocument || stored.ExtractionComplete {
			continue
		}
		tag, err := tx.Exec(ctx,
			`UPDATE message_attachments
			    SET extracted_markdown = $1, extraction_complete = TRUE
			  WHERE id = $2 AND message_id = $3 AND kind = $4`,
			extracted.ExtractedMarkdown, stored.ID, message.ID,
			model.AttachmentKindDocument,
		)
		if err != nil {
			return model.Message{}, fmt.Errorf("update message attachment extraction: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return model.Message{}, ErrNotFound
		}
		stored.ExtractedMarkdown = extracted.ExtractedMarkdown
		stored.ExtractionComplete = true
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit update chat attachment extractions: %w", err)
	}
	return message, nil
}

func insertMessageAttachments(
	ctx context.Context, tx pgx.Tx, messageID int64, attachments []model.MessageAttachment,
) ([]model.MessageAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	out := make([]model.MessageAttachment, 0, len(attachments))
	for ordinal, attachment := range attachments {
		var stored model.MessageAttachment
		err := tx.QueryRow(ctx,
			`INSERT INTO message_attachments (
			     message_id, filename, mime_type, kind, size_bytes, raw_bytes,
			     extracted_markdown, extraction_complete, image_width, image_height, ordinal
			 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 RETURNING id, message_id, filename, mime_type, kind, size_bytes,
			           extraction_complete, image_width, image_height, ordinal`,
			messageID, attachment.Filename, attachment.MIME, attachment.Kind,
			int64(len(attachment.RawBytes)), attachment.RawBytes, attachment.ExtractedMarkdown,
			attachment.ExtractionComplete, attachment.ImageWidth, attachment.ImageHeight, ordinal,
		).Scan(
			&stored.ID, &stored.MessageID, &stored.Filename, &stored.MIME,
			&stored.Kind, &stored.SizeBytes, &stored.ExtractionComplete,
			&stored.ImageWidth, &stored.ImageHeight, &stored.Ordinal,
		)
		if err != nil {
			return nil, fmt.Errorf("insert message attachment: %w", err)
		}
		stored.RawBytes = attachment.RawBytes
		stored.ExtractedMarkdown = attachment.ExtractedMarkdown
		out = append(out, stored)
	}
	return out, nil
}

type visibleDocumentSnapshot struct {
	id       int64
	filename string
	scope    string
}

func insertMessageDocumentReferences(
	ctx context.Context, tx pgx.Tx, messageID, userID int64, documentIDs []int64,
) ([]model.MessageDocumentReference, error) {
	if len(documentIDs) == 0 {
		return nil, nil
	}
	documents, err := loadVisibleDocumentSnapshots(ctx, tx, userID, documentIDs)
	if err != nil {
		return nil, err
	}
	out := make([]model.MessageDocumentReference, 0, len(documents))
	for ordinal, document := range documents {
		var reference model.MessageDocumentReference
		err := tx.QueryRow(ctx,
			`INSERT INTO message_document_references (
			     message_id, document_id, filename_snapshot, scope_snapshot, ordinal
			 ) VALUES ($1, $2, $3, $4, $5)
			 RETURNING id, message_id, document_id, filename_snapshot, scope_snapshot, ordinal`,
			messageID, document.id, document.filename, document.scope, ordinal,
		).Scan(
			&reference.ID, &reference.MessageID, &reference.DocumentID,
			&reference.Filename, &reference.Scope, &reference.Ordinal,
		)
		if err != nil {
			if isDocumentReferenceForeignKeyViolation(err) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("insert message document reference: %w", err)
		}
		reference.Available = true
		out = append(out, reference)
	}
	return out, nil
}

func loadVisibleDocumentSnapshots(
	ctx context.Context, tx pgx.Tx, userID int64, documentIDs []int64,
) ([]visibleDocumentSnapshot, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, filename, scope
		   FROM documents
		  WHERE id = ANY($1::bigint[])
		    AND (scope = $2 OR (scope = $3 AND owner_user_id = $4))
		  ORDER BY array_position($1::bigint[], id)
		  FOR KEY SHARE`,
		documentIDs, model.ScopePublic, model.ScopePrivate, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("load visible document references: %w", err)
	}
	defer rows.Close()

	documents := make([]visibleDocumentSnapshot, 0, len(documentIDs))
	for rows.Next() {
		var document visibleDocumentSnapshot
		if err := rows.Scan(&document.id, &document.filename, &document.scope); err != nil {
			return nil, fmt.Errorf("scan visible document reference: %w", err)
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load visible document references: %w", err)
	}
	if len(documents) != len(documentIDs) {
		return nil, ErrNotFound
	}
	return documents, nil
}

func isDocumentReferenceForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23503" &&
		pgErr.ConstraintName == "message_document_references_document_id_fkey"
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
		`WITH inserted AS (
		     INSERT INTO messages (conversation_id, role, content, tool_calls, purpose)
		     VALUES ($1::uuid, $2, $3, $4, $5)
		     RETURNING id, conversation_id, role, content, tool_calls, created_at
		 ), touched AS (
		     UPDATE conversations
		        SET last_activity_at = GREATEST(
		            last_activity_at,
		            (SELECT created_at FROM inserted)
		        )
		      WHERE id = $1::uuid AND kind = $6 AND $5 = $6
		 )
		 SELECT id, conversation_id::text, role, content, tool_calls, created_at FROM inserted`,
		conversationID, role, content, raw, purpose, messagePurposeChat).
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
		`SELECT id, conversation_id::text, role, content, tool_calls, purpose, created_at FROM messages
		 WHERE conversation_id = $1::uuid ORDER BY id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if err := hydrateMessageRelations(ctx, r.pool, messages, false); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListChatHistory returns provider-facing chat history metadata. Attachment
// payloads are loaded separately after the service has bounded retained turns.
func (r *MessageRepository) ListChatHistory(
	ctx context.Context, conversationID string,
) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT m.id, m.conversation_id::text, m.role, m.content, m.tool_calls, m.purpose, m.created_at
		   FROM messages AS m
		   JOIN conversations AS c ON c.id = m.conversation_id
		  WHERE m.conversation_id = $1::uuid
		    AND (
		        m.purpose = $2
		        OR (c.kind = $3 AND m.purpose IN ('scheduled_delivery', 'scheduled_definition'))
		    )
		  ORDER BY m.id`,
		conversationID, messagePurposeChat, model.ConversationKindChat,
	)
	if err != nil {
		return nil, fmt.Errorf("list chat history: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if err := hydrateMessageRelations(ctx, r.pool, messages, false); err != nil {
		return nil, err
	}
	return messages, nil
}

// LoadChatAttachmentPayloads loads ordered attachment payloads for selected
// ordinary user messages in one conversation. Every requested message must
// match the conversation and have at least one attachment.
func (r *MessageRepository) LoadChatAttachmentPayloads(
	ctx context.Context, conversationID string, messageIDs []int64,
) (map[int64][]model.MessageAttachment, error) {
	return r.loadChatAttachmentPayloads(ctx, conversationID, messageIDs, false)
}

// LoadChatAttachmentProviderPayloads loads only provider-facing payload
// columns: image bytes or extracted document markdown.
func (r *MessageRepository) LoadChatAttachmentProviderPayloads(
	ctx context.Context, conversationID string, messageIDs []int64,
) (map[int64][]model.MessageAttachment, error) {
	return r.loadChatAttachmentPayloads(ctx, conversationID, messageIDs, true)
}

func (r *MessageRepository) loadChatAttachmentPayloads(
	ctx context.Context, conversationID string, messageIDs []int64,
	providerOnly bool,
) (map[int64][]model.MessageAttachment, error) {
	if len(messageIDs) == 0 {
		return map[int64][]model.MessageAttachment{}, nil
	}
	requested := make(map[int64]struct{}, len(messageIDs))
	uniqueIDs := make([]int64, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		if messageID <= 0 {
			return nil, ErrNotFound
		}
		if _, exists := requested[messageID]; exists {
			continue
		}
		requested[messageID] = struct{}{}
		uniqueIDs = append(uniqueIDs, messageID)
	}

	payloadColumns := `a.raw_bytes, a.extracted_markdown`
	if providerOnly {
		payloadColumns = `CASE WHEN a.kind = 'image' THEN a.raw_bytes ELSE ''::bytea END,
		                  CASE WHEN a.kind = 'document' THEN a.extracted_markdown ELSE '' END`
	}
	rows, err := r.pool.Query(ctx,
		`SELECT a.id, a.message_id, a.filename, a.mime_type, a.kind,
		        a.size_bytes, a.extraction_complete, a.image_width,
		        a.image_height, a.ordinal,
		        CASE WHEN a.kind = 'image'
		             THEN octet_length(a.raw_bytes)
		             ELSE octet_length(a.extracted_markdown)
		        END AS payload_bytes,
		        `+payloadColumns+`
		   FROM messages m
		   JOIN message_attachments a ON a.message_id = m.id
		  WHERE m.conversation_id = $1::uuid
		    AND m.purpose = $2
		    AND m.role = $3
		    AND m.id = ANY($4::bigint[])
		  ORDER BY m.id, a.ordinal`,
		conversationID, messagePurposeChat, model.MsgRoleUser, uniqueIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("load chat attachment payloads: %w", err)
	}
	defer rows.Close()

	payloads := make(map[int64][]model.MessageAttachment, len(uniqueIDs))
	for rows.Next() {
		var attachment model.MessageAttachment
		if err := rows.Scan(
			&attachment.ID, &attachment.MessageID, &attachment.Filename,
			&attachment.MIME, &attachment.Kind, &attachment.SizeBytes,
			&attachment.ExtractionComplete, &attachment.ImageWidth,
			&attachment.ImageHeight, &attachment.Ordinal,
			&attachment.PayloadBytes,
			&attachment.RawBytes, &attachment.ExtractedMarkdown,
		); err != nil {
			return nil, fmt.Errorf("scan chat attachment payload: %w", err)
		}
		payloads[attachment.MessageID] = append(
			payloads[attachment.MessageID], attachment,
		)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load chat attachment payloads: %w", err)
	}
	if len(payloads) != len(uniqueIDs) {
		return nil, ErrNotFound
	}
	return payloads, nil
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
	messages := []model.Message{message}
	if err := hydrateMessageRelations(ctx, r.pool, messages, false); err != nil {
		return model.Message{}, err
	}
	return messages[0], nil
}

// ListRecentByConversation returns at most limit newest messages while
// preserving chronological order in the returned conversation history.
func (r *MessageRepository) ListRecentByConversation(ctx context.Context, conversationID string, limit int) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, purpose, created_at
		   FROM (
		        SELECT id, conversation_id::text, role, content, tool_calls, purpose, created_at
		          FROM messages
		         WHERE conversation_id = $1::uuid
		         ORDER BY id DESC
		         LIMIT $2
		   ) AS recent
		  ORDER BY id`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent messages: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if err := hydrateMessageRelations(ctx, r.pool, messages, false); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListRecentDefinitionByConversation returns only Scheduled definition
// exchanges. Unattended delivery messages remain visible in the conversation
// and run history but cannot consume the definition compiler's bounded context.
func (r *MessageRepository) ListRecentDefinitionByConversation(ctx context.Context, conversationID string, limit int) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, purpose, created_at
		   FROM (
		        SELECT id, conversation_id::text, role, content, tool_calls, purpose, created_at
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
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if err := hydrateMessageRelations(ctx, r.pool, messages, false); err != nil {
		return nil, err
	}
	return messages, nil
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
	if err := touchChatConversation(ctx, tx, conversationID); err != nil {
		return model.Message{}, err
	}
	targets := []model.Message{target}
	if err := hydrateMessageRelations(ctx, tx, targets, true); err != nil {
		return model.Message{}, err
	}
	target = targets[0]
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
	if err := touchChatConversation(ctx, tx, conversationID); err != nil {
		return model.Message{}, err
	}
	prompts := []model.Message{prompt}
	if err := hydrateMessageRelations(ctx, tx, prompts, true); err != nil {
		return model.Message{}, err
	}
	prompt = prompts[0]
	if err := tx.Commit(ctx); err != nil {
		return model.Message{}, fmt.Errorf("commit regenerate rewind: %w", err)
	}
	return prompt, nil
}

// DeleteUserAndRewind removes one owned ordinary-chat user message and its
// suffix. Deleting the first chat message deletes the whole conversation.
func (r *MessageRepository) DeleteUserAndRewind(
	ctx context.Context, conversationID string, messageID, userID int64,
) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin delete user rewind: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedChat(ctx, tx, conversationID, userID); err != nil {
		return false, err
	}
	target, err := getMessageForRewind(ctx, tx, conversationID, messageID)
	if err != nil {
		return false, err
	}
	if target.Role != model.MsgRoleUser {
		return false, ErrWrongMessageRole
	}
	var hasEarlierMessage bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM messages
		      WHERE conversation_id = $1::uuid AND purpose = $2 AND id < $3
		 )`,
		conversationID, messagePurposeChat, messageID,
	).Scan(&hasEarlierMessage); err != nil {
		return false, fmt.Errorf("check earlier chat messages: %w", err)
	}
	if !hasEarlierMessage {
		if _, err := tx.Exec(ctx,
			`DELETE FROM conversations WHERE id = $1::uuid AND user_id = $2`,
			conversationID, userID,
		); err != nil {
			return false, fmt.Errorf("delete first-message conversation: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit delete first-message conversation: %w", err)
		}
		return true, nil
	}
	if err := cleanupDraftHandoffsForSourceMessages(ctx, tx, userID, conversationID, messageID); err != nil {
		return false, err
	}
	if err := deleteMessageChunksFrom(ctx, tx, conversationID, messageID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM messages WHERE conversation_id = $1::uuid AND id >= $2`,
		conversationID, messageID,
	); err != nil {
		return false, fmt.Errorf("delete user message suffix: %w", err)
	}
	if err := touchChatConversation(ctx, tx, conversationID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit delete user rewind: %w", err)
	}
	return false, nil
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

func touchChatConversation(ctx context.Context, tx pgx.Tx, conversationID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE conversations
		    SET last_activity_at = GREATEST(last_activity_at, clock_timestamp())
		  WHERE id = $1::uuid`, conversationID); err != nil {
		return fmt.Errorf("touch chat conversation activity: %w", err)
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
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &tcRaw, &m.Purpose, &m.CreatedAt); err != nil {
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

type messageRelationsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func hydrateMessageRelations(
	ctx context.Context, db messageRelationsQuerier, messages []model.Message,
	includeAttachmentPayload bool,
) error {
	if len(messages) == 0 {
		return nil
	}
	messageIndexes := make(map[int64]int, len(messages))
	messageIDs := make([]int64, 0, len(messages))
	for index := range messages {
		messageIndexes[messages[index].ID] = index
		messageIDs = append(messageIDs, messages[index].ID)
	}

	attachmentQuery := `SELECT id, message_id, filename, mime_type, kind, size_bytes,
	                           extraction_complete, image_width, image_height, ordinal,
	                           CASE WHEN kind = 'image'
	                                THEN octet_length(raw_bytes)
	                                ELSE octet_length(extracted_markdown)
	                           END AS payload_bytes`
	if includeAttachmentPayload {
		attachmentQuery += `, raw_bytes, extracted_markdown`
	}
	attachmentQuery += `
	                      FROM message_attachments
	                     WHERE message_id = ANY($1::bigint[])
	                     ORDER BY message_id, ordinal`
	attachmentRows, err := db.Query(ctx, attachmentQuery, messageIDs)
	if err != nil {
		return fmt.Errorf("list message attachments: %w", err)
	}
	for attachmentRows.Next() {
		var attachment model.MessageAttachment
		destinations := []any{
			&attachment.ID, &attachment.MessageID, &attachment.Filename,
			&attachment.MIME, &attachment.Kind, &attachment.SizeBytes,
			&attachment.ExtractionComplete, &attachment.ImageWidth,
			&attachment.ImageHeight, &attachment.Ordinal, &attachment.PayloadBytes,
		}
		if includeAttachmentPayload {
			destinations = append(
				destinations, &attachment.RawBytes, &attachment.ExtractedMarkdown,
			)
		}
		if err := attachmentRows.Scan(destinations...); err != nil {
			attachmentRows.Close()
			return fmt.Errorf("scan message attachment: %w", err)
		}
		index, ok := messageIndexes[attachment.MessageID]
		if !ok {
			attachmentRows.Close()
			return fmt.Errorf("scan message attachment: message %d was not requested", attachment.MessageID)
		}
		messages[index].Attachments = append(messages[index].Attachments, attachment)
	}
	if err := attachmentRows.Err(); err != nil {
		attachmentRows.Close()
		return fmt.Errorf("list message attachments: %w", err)
	}
	attachmentRows.Close()

	referenceRows, err := db.Query(ctx,
		`SELECT r.id, r.message_id, r.document_id, r.filename_snapshot,
		        r.scope_snapshot, r.ordinal,
		        COALESCE(octet_length(d.extracted_markdown), 0)
		   FROM message_document_references r
		   LEFT JOIN documents d ON d.id = r.document_id
		  WHERE r.message_id = ANY($1::bigint[])
		  ORDER BY r.message_id, r.ordinal`,
		messageIDs,
	)
	if err != nil {
		return fmt.Errorf("list message document references: %w", err)
	}
	for referenceRows.Next() {
		var reference model.MessageDocumentReference
		if err := referenceRows.Scan(
			&reference.ID, &reference.MessageID, &reference.DocumentID,
			&reference.Filename, &reference.Scope, &reference.Ordinal,
			&reference.PayloadBytes,
		); err != nil {
			referenceRows.Close()
			return fmt.Errorf("scan message document reference: %w", err)
		}
		reference.Available = reference.DocumentID != nil
		index, ok := messageIndexes[reference.MessageID]
		if !ok {
			referenceRows.Close()
			return fmt.Errorf(
				"scan message document reference: message %d was not requested",
				reference.MessageID,
			)
		}
		messages[index].DocumentReferences = append(messages[index].DocumentReferences, reference)
	}
	if err := referenceRows.Err(); err != nil {
		referenceRows.Close()
		return fmt.Errorf("list message document references: %w", err)
	}
	referenceRows.Close()
	return nil
}
