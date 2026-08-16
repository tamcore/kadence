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

// messageCols is the column list scanMessageRow reads; messagePurposeCols adds
// the purpose column scanMessages reads. Keep each in step with its scanner.
const (
	messageCols            = "id, conversation_id::text, role, content, tool_calls, created_at"
	messageColsWithPurpose = "id, conversation_id::text, role, content, tool_calls, purpose, created_at"
)

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
	return inTx(ctx, r.pool, "add chat user", func(tx pgx.Tx) (model.Message, error) {
		if err := lockChatConversation(ctx, tx, conversationID); err != nil {
			return model.Message{}, err
		}
		return addMessageWithPurpose(ctx, tx, conversationID, model.MsgRoleUser, content, nil, messagePurposeChat)
	})
}

// AddChatUserInput atomically appends one ordinary chat user message and its
// attachments/document references while holding the owned conversation lock.
func (r *MessageRepository) AddChatUserInput(
	ctx context.Context, conversationID string, userID int64, input model.ChatUserInput,
) (model.Message, error) {
	return inTx(ctx, r.pool, "add chat user input", func(tx pgx.Tx) (model.Message, error) {
		if err := lockOwnedChat(ctx, tx, conversationID, userID); err != nil {
			return model.Message{}, err
		}
		return insertChatUserInput(ctx, tx, conversationID, userID, input)
	})
}

// CreateConversationWithChatUserInput atomically creates one ordinary chat
// conversation and its first rich user input. A failure while inserting the
// message, attachments, or document references rolls the conversation back.
func (r *MessageRepository) CreateConversationWithChatUserInput(
	ctx context.Context, userID int64, title string, input model.ChatUserInput,
) (model.Conversation, model.Message, error) {
	type created struct {
		conversation model.Conversation
		message      model.Message
	}
	out, err := inTx(ctx, r.pool, "create conversation with chat user input",
		func(tx pgx.Tx) (created, error) {
			conversation, err := insertConversation(ctx, tx, userID, title, model.ConversationKindChat)
			if err != nil {
				return created{}, err
			}
			message, err := insertChatUserInput(ctx, tx, conversation.ID, userID, input)
			if err != nil {
				return created{}, err
			}
			return created{conversation: conversation, message: message}, nil
		})
	if err != nil {
		return model.Conversation{}, model.Message{}, err
	}
	return out.conversation, out.message, nil
}

// insertChatUserInput writes one ordinary-chat user message plus its
// attachments and document references. The caller supplies the transaction and
// is responsible for having taken the conversation lock.
func insertChatUserInput(
	ctx context.Context, tx pgx.Tx, conversationID string, userID int64, input model.ChatUserInput,
) (model.Message, error) {
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
	return message, nil
}

// UpdateChatAttachmentExtractions atomically persists deferred document
// extraction results for one owned ordinary-chat user message.
func (r *MessageRepository) UpdateChatAttachmentExtractions(
	ctx context.Context, conversationID string, messageID, userID int64,
	attachments []model.MessageAttachment,
) (model.Message, error) {
	return inTx(ctx, r.pool, "update chat attachment extractions", func(tx pgx.Tx) (model.Message, error) {
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
		return message, nil
	})
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
	return inTx(ctx, r.pool, "add chat assistant", func(tx pgx.Tx) (model.Message, error) {
		if err := lockChatConversation(ctx, tx, conversationID); err != nil {
			return model.Message{}, err
		}
		var latestID int64
		var latestRole, latestContent string
		err := tx.QueryRow(ctx,
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
		return message, nil
	})
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
		 SELECT `+messageCols+` FROM inserted`,
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

// queryHydratedMessages runs one message query and returns fully hydrated rows:
// scanned, then joined to their attachments and document references. Every
// message list read goes through it so hydration cannot be forgotten.
func (r *MessageRepository) queryHydratedMessages(
	ctx context.Context, wrap string, sql string, args ...any,
) ([]model.Message, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", wrap, err)
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
