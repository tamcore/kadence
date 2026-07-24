-- +goose Up
ALTER TABLE messages ADD COLUMN purpose TEXT;

-- Scheduled definition and delivery messages shared one conversation before
-- purpose was persisted. Start by classifying every legacy Scheduled message
-- as definition context, then identify deliveries using the run row written in
-- the same transaction. PostgreSQL's NOW() is transaction-stable, so the
-- message created_at and run finished_at values are identical for that write;
-- matching the result as well avoids misclassifying an earlier definition
-- response that happens to have the same text.
UPDATE messages AS message
   SET purpose = 'scheduled_definition'
  FROM conversations AS conversation
 WHERE conversation.id = message.conversation_id
   AND conversation.kind = 'scheduled';

UPDATE messages AS message
   SET purpose = 'scheduled_delivery'
 WHERE message.role = 'assistant'
   AND EXISTS (
       SELECT 1
         FROM scheduled_task_runs AS run
         JOIN scheduled_tasks AS task ON task.id = run.task_id
        WHERE task.conversation_id = message.conversation_id
          AND run.state IN ('delivered', 'completed')
          AND run.finished_at = message.created_at
          AND run.result = message.content
   );

UPDATE messages SET purpose = 'chat' WHERE purpose IS NULL;

ALTER TABLE messages ALTER COLUMN purpose SET DEFAULT 'chat';
ALTER TABLE messages ALTER COLUMN purpose SET NOT NULL;
ALTER TABLE messages ADD CONSTRAINT messages_purpose_check
    CHECK (purpose IN ('chat', 'scheduled_definition', 'scheduled_delivery'));

CREATE INDEX idx_messages_scheduled_definition
    ON messages(conversation_id, id DESC)
    WHERE purpose = 'scheduled_definition';

-- +goose Down
DROP INDEX idx_messages_scheduled_definition;
ALTER TABLE messages DROP COLUMN purpose;
