-- +goose Up
-- 门店 AI 分类器的每次判断（ADR-0015）：代码规则和模型各记一条，作为调档位和条数的评测数据。
CREATE TABLE ai_classifier_checks (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 inbox_id uuid NOT NULL,
 session_id uuid,
 tool text NOT NULL CHECK (btrim(tool) <> ''),
 args jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(args) = 'object'),
 decision text NOT NULL CHECK (decision IN ('allow','block')),
 source text NOT NULL CHECK (source IN ('rule','model')),
 reason text CHECK (decision = 'allow' OR (reason IS NOT NULL AND btrim(reason) <> '')),
 model_reply text,
 created_at timestamptz NOT NULL,
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, inbox_id) REFERENCES ai_inboxes (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id, session_id) REFERENCES ai_sessions (tenant_id, store_id, id)
);
CREATE INDEX ai_classifier_checks_inbox ON ai_classifier_checks (store_id, inbox_id, created_at, id);
ALTER TABLE ai_classifier_checks ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_classifier_checks
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON ai_classifier_checks TO salon_app;

-- +goose Down
DROP TABLE ai_classifier_checks;
