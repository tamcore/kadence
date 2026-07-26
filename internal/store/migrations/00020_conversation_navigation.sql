-- +goose Up
ALTER TABLE conversations
    ADD COLUMN pinned_at TIMESTAMPTZ,
    ADD COLUMN last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE conversations AS conversation
   SET last_activity_at = COALESCE(
       (
           SELECT message.created_at
             FROM messages AS message
            WHERE message.conversation_id = conversation.id
            ORDER BY message.created_at DESC, message.id DESC
            LIMIT 1
       ),
       conversation.created_at
   );

-- +goose Down
ALTER TABLE conversations
    DROP COLUMN last_activity_at,
    DROP COLUMN pinned_at;
