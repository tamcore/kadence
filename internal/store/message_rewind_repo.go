package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tamcore/kadence/internal/model"
)

// Destructive chat rewinds: edit, regenerate, and delete each drop the message
// suffix after a target message, plus the RAG chunks derived from it.

// EditAndRewind updates one owned ordinary-chat user message and removes every
// later message plus every RAG chunk derived from the rewritten suffix.
func (r *MessageRepository) EditAndRewind(
	ctx context.Context, conversationID string, messageID, userID int64, content string,
) (model.Message, error) {
	return inTx(ctx, r.pool, "edit rewind", func(tx pgx.Tx) (model.Message, error) {
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
		return target, nil
	})
}

// RegenerateAndRewind removes one owned ordinary-chat assistant message and
// every later message plus their derived RAG chunks, returning its preceding
// user prompt for regeneration.
func (r *MessageRepository) RegenerateAndRewind(
	ctx context.Context, conversationID string, messageID, userID int64,
) (model.Message, error) {
	return inTx(ctx, r.pool, "regenerate rewind", func(tx pgx.Tx) (model.Message, error) {
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
			`SELECT `+messageCols+`
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
		return prompt, nil
	})
}

// DeleteUserAndRewind removes one owned ordinary-chat user message and its
// suffix. Deleting the first chat message deletes the whole conversation.
func (r *MessageRepository) DeleteUserAndRewind(
	ctx context.Context, conversationID string, messageID, userID int64,
) (bool, error) {
	return inTx(ctx, r.pool, "delete user rewind", func(tx pgx.Tx) (bool, error) {
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
			if err := cleanupDraftHandoffsForConversation(ctx, tx, userID, conversationID); err != nil {
				return false, err
			}
			if _, err := tx.Exec(ctx,
				`DELETE FROM conversations WHERE id = $1::uuid AND user_id = $2`,
				conversationID, userID,
			); err != nil {
				if isDeliveryConversationForeignKeyViolation(err) {
					return false, ErrConversationHasActiveDelivery
				}
				return false, fmt.Errorf("delete first-message conversation: %w", err)
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
		return false, nil
	})
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
		`SELECT `+messageCols+`
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
