package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tamcore/kadence/internal/model"
)

// GetAttachmentForUser returns an attachment payload only when the user,
// conversation, message, and attachment path all match.
func (r *MessageRepository) GetAttachmentForUser(
	ctx context.Context,
	userID int64,
	conversationID string,
	messageID int64,
	attachmentID int64,
) (model.MessageAttachment, error) {
	var attachment model.MessageAttachment
	err := r.pool.QueryRow(ctx,
		`SELECT attachment.id, attachment.message_id, attachment.filename,
		        attachment.mime_type, attachment.kind, attachment.size_bytes,
		        attachment.raw_bytes, attachment.image_width,
		        attachment.image_height, attachment.ordinal
		   FROM message_attachments AS attachment
		   JOIN messages AS message ON message.id = attachment.message_id
		   JOIN conversations AS conversation ON conversation.id = message.conversation_id
		  WHERE conversation.user_id = $1
		    AND conversation.id = $2::uuid
		    AND conversation.kind = $3
		    AND message.id = $4
		    AND message.role = $5
		    AND message.purpose = $6
		    AND attachment.id = $7`,
		userID, conversationID, model.ConversationKindChat, messageID,
		model.MsgRoleUser, messagePurposeChat, attachmentID,
	).Scan(
		&attachment.ID, &attachment.MessageID, &attachment.Filename,
		&attachment.MIME, &attachment.Kind, &attachment.SizeBytes,
		&attachment.RawBytes, &attachment.ImageWidth, &attachment.ImageHeight,
		&attachment.Ordinal,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.MessageAttachment{}, ErrNotFound
	}
	if err != nil {
		return model.MessageAttachment{}, fmt.Errorf("get owned message attachment: %w", err)
	}
	return attachment, nil
}
