-- +goose Up
CREATE TABLE ai_conversations (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 title text NOT NULL,
 archived_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
CREATE INDEX ai_conversations_list ON ai_conversations (store_id, staff_id, updated_at DESC, id);
ALTER TABLE ai_conversations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_conversations
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_conversation_messages (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 conversation_id uuid NOT NULL,
 ordinal bigint GENERATED ALWAYS AS IDENTITY,
 role text NOT NULL CHECK (role IN ('user','assistant')),
 content text NOT NULL,
 sent text,
 editing text,
 draft_ids uuid[] NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL,
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, conversation_id) REFERENCES ai_conversations (tenant_id, store_id, id)
);
CREATE INDEX ai_conversation_messages_list ON ai_conversation_messages (conversation_id, ordinal);
ALTER TABLE ai_conversation_messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_conversation_messages
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON ai_conversations TO salon_app;
GRANT UPDATE (archived_at, updated_at) ON ai_conversations TO salon_app;
GRANT SELECT, INSERT ON ai_conversation_messages TO salon_app;

-- +goose Down
DROP TABLE ai_conversation_messages;
DROP TABLE ai_conversations;
