-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Destructive: drop all existing conversations (cascades to messages + message-sourced
-- chunks; document chunks with NULL conversation_id survive).
DELETE FROM conversations;

ALTER TABLE messages DROP CONSTRAINT messages_conversation_id_fkey;
ALTER TABLE messages ALTER COLUMN conversation_id TYPE UUID USING NULL;

ALTER TABLE chunks DROP CONSTRAINT chunks_conversation_id_fkey;
ALTER TABLE chunks ALTER COLUMN conversation_id TYPE UUID USING NULL;

ALTER TABLE conversations DROP CONSTRAINT conversations_pkey;
ALTER TABLE conversations DROP COLUMN id;
ALTER TABLE conversations ADD COLUMN id UUID PRIMARY KEY DEFAULT gen_random_uuid();

ALTER TABLE messages
  ADD CONSTRAINT messages_conversation_id_fkey
  FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;
ALTER TABLE chunks
  ADD CONSTRAINT chunks_conversation_id_fkey
  FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_chunks_conversation_id ON chunks(conversation_id);

-- +goose Down
DELETE FROM conversations;
ALTER TABLE messages DROP CONSTRAINT messages_conversation_id_fkey;
ALTER TABLE messages ALTER COLUMN conversation_id TYPE BIGINT USING NULL;
ALTER TABLE chunks DROP CONSTRAINT chunks_conversation_id_fkey;
ALTER TABLE chunks ALTER COLUMN conversation_id TYPE BIGINT USING NULL;
ALTER TABLE conversations DROP CONSTRAINT conversations_pkey;
ALTER TABLE conversations DROP COLUMN id;
ALTER TABLE conversations ADD COLUMN id BIGSERIAL PRIMARY KEY;
ALTER TABLE messages ADD CONSTRAINT messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;
ALTER TABLE chunks ADD CONSTRAINT chunks_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_chunks_conversation_id ON chunks(conversation_id);
