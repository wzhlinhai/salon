-- +goose Up
CREATE TABLE ai_drafts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL, store_id uuid NOT NULL,
 initiator uuid NOT NULL, operation text NOT NULL, utterance text NOT NULL, params jsonb NOT NULL,
 original_params jsonb NOT NULL, summary text NOT NULL,
 status text NOT NULL CHECK (status IN ('pending','confirmed','rejected','expired','confirmed_modified')),
 business_date date NOT NULL, expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL,
 decided_at timestamptz, decided_by uuid, reject_reason text,
 UNIQUE (tenant_id, id), UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, initiator) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, decided_by) REFERENCES staff (tenant_id, id)
);
CREATE TABLE ai_draft_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL, store_id uuid NOT NULL,
 draft_id uuid NOT NULL, ordinal bigint GENERATED ALWAYS AS IDENTITY, kind text NOT NULL CHECK (kind IN ('created','modified','confirmed','rejected','expired')),
 before_params jsonb, after_params jsonb NOT NULL, actor_id uuid NOT NULL, at timestamptz NOT NULL,
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, draft_id) REFERENCES ai_drafts (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, actor_id) REFERENCES staff (tenant_id, id)
);
ALTER TABLE ai_drafts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_drafts
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE ai_draft_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_draft_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON ai_drafts, ai_draft_events TO salon_app;
GRANT UPDATE (params,status,decided_at,decided_by,reject_reason) ON ai_drafts TO salon_app;
CREATE INDEX ai_drafts_store ON ai_drafts (store_id, created_at, id);
CREATE INDEX ai_draft_events_draft ON ai_draft_events (draft_id, at, id);
-- +goose Down
DROP TABLE ai_draft_events;
DROP TABLE ai_drafts;
