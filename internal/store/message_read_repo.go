package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tamcore/kadence/internal/model"
)

// Message reads: conversation history, single lookups, and attachment payload
// loading. Writes live in message_repo.go, rewinds in message_rewind_repo.go.

// ListByConversation returns a conversation's messages in chronological order.
func (r *MessageRepository) ListByConversation(ctx context.Context, conversationID string) ([]model.Message, error) {
	return r.queryHydratedMessages(ctx, "list messages",
		`SELECT `+messageColsWithPurpose+` FROM messages
		 WHERE conversation_id = $1::uuid ORDER BY id`, conversationID)
}

// ListChatHistory returns provider-facing chat history metadata. Attachment
// payloads are loaded separately after the service has bounded retained turns.
func (r *MessageRepository) ListChatHistory(
	ctx context.Context, conversationID string,
) ([]model.Message, error) {
	return r.queryHydratedMessages(ctx, "list chat history",
		`SELECT m.id, m.conversation_id::text, m.role, m.content, m.tool_calls, m.purpose, m.created_at
		   FROM messages AS m
		   JOIN conversations AS c ON c.id = m.conversation_id
		  WHERE m.conversation_id = $1::uuid
		    AND (
		        m.purpose = $2
		        OR (c.kind = $3 AND m.purpose IN ('scheduled_delivery', 'scheduled_definition'))
		    )
		  ORDER BY m.id`,
		conversationID, messagePurposeChat, model.ConversationKindChat)
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
		`SELECT `+messageCols+`
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

// ListRecentDefinitionByConversation returns only Scheduled definition
// exchanges. Unattended delivery messages remain visible in the conversation
// and run history but cannot consume the definition compiler's bounded context.
func (r *MessageRepository) ListRecentDefinitionByConversation(ctx context.Context, conversationID string, limit int) ([]model.Message, error) {
	return r.queryHydratedMessages(ctx, "list recent scheduled definition messages",
		`SELECT `+messageColsWithPurpose+`
		   FROM (
		        SELECT `+messageColsWithPurpose+`
		          FROM messages
		         WHERE conversation_id = $1::uuid AND purpose = $2
		         ORDER BY id DESC
		         LIMIT $3
		   ) AS recent
		  ORDER BY id`,
		conversationID, messagePurposeScheduledDefinition, limit)
}
