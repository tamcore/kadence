-- +goose Up
ALTER TABLE scheduled_tasks
    ADD COLUMN delivery_conversation_id UUID REFERENCES conversations(id) ON DELETE RESTRICT;

-- Chat-originated schedules deliver into the source chat conversation.
UPDATE scheduled_tasks AS task
   SET delivery_conversation_id = h.source_conversation_id
  FROM chat_scheduled_handoffs AS h
 WHERE h.scheduled_task_id = task.id;

-- Direct (non-handoff), confirmed schedules deliver into their own conversation.
UPDATE scheduled_tasks AS task
   SET delivery_conversation_id = task.conversation_id
 WHERE task.state <> 'draft'
   AND task.delivery_conversation_id IS NULL
   AND NOT EXISTS (
       SELECT 1 FROM chat_scheduled_handoffs AS h WHERE h.scheduled_task_id = task.id
   );

-- Promote each direct schedule's own conversation to a continuable chat.
UPDATE conversations AS c
   SET kind = 'chat'
 WHERE c.kind = 'scheduled'
   AND EXISTS (
       SELECT 1 FROM scheduled_tasks AS task
        WHERE task.conversation_id = c.id
          AND task.delivery_conversation_id = c.id
   );

-- Move past delivery messages from the (now vestigial) scheduled conversation
-- to the resolved chat delivery conversation, where they differ.
UPDATE messages AS m
   SET conversation_id = task.delivery_conversation_id
  FROM scheduled_tasks AS task
 WHERE m.conversation_id = task.conversation_id
   AND m.purpose = 'scheduled_delivery'
   AND task.delivery_conversation_id IS NOT NULL
   AND task.delivery_conversation_id <> task.conversation_id;

-- +goose Down
ALTER TABLE scheduled_tasks DROP COLUMN delivery_conversation_id;
