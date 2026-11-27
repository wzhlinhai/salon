-- +goose Up
-- 门店 AI 骨架：收件箱、事件、会话、定时器、记忆、发件箱和交办（ADR-0015）。

CREATE TABLE ai_inboxes (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid,
 lease_token uuid,
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
CREATE UNIQUE INDEX ai_inboxes_store_unique ON ai_inboxes (tenant_id, store_id) WHERE staff_id IS NULL;
CREATE UNIQUE INDEX ai_inboxes_staff_unique ON ai_inboxes (tenant_id, staff_id) WHERE staff_id IS NOT NULL;
ALTER TABLE ai_inboxes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_inboxes
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_sessions (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 inbox_id uuid NOT NULL,
 subject_kind text,
 subject_id uuid,
 status text NOT NULL CHECK (status IN ('active','waiting','closed')),
 waiting_for text,
 messages jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(messages) = 'array'),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK ((subject_kind IS NULL) = (subject_id IS NULL)),
 CHECK (subject_kind IS NULL OR subject_kind <> ''),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, inbox_id) REFERENCES ai_inboxes (tenant_id, store_id, id)
);
CREATE UNIQUE INDEX ai_sessions_open_unique ON ai_sessions (tenant_id, store_id, inbox_id, subject_kind, subject_id)
 WHERE status <> 'closed';
ALTER TABLE ai_sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_sessions
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_inbox_events (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 inbox_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_]{0,63}$'),
 source_key text NOT NULL CHECK (source_key <> ''),
 subject_kind text,
 subject_id uuid,
 payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
 status text NOT NULL CHECK (status IN ('pending','running','done','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 not_before timestamptz NOT NULL,
 last_error text,
 lease_token uuid,
 session_id uuid,
 created_at timestamptz NOT NULL,
 done_at timestamptz,
 CHECK ((subject_kind IS NULL) = (subject_id IS NULL)),
 CHECK (subject_kind IS NULL OR subject_kind <> ''),
 UNIQUE (tenant_id, id),
 UNIQUE (inbox_id, source_key),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, inbox_id) REFERENCES ai_inboxes (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id, session_id) REFERENCES ai_sessions (tenant_id, store_id, id)
);
CREATE INDEX ai_inbox_events_pending ON ai_inbox_events (inbox_id, status, not_before, created_at, id);
CREATE INDEX ai_inbox_events_session ON ai_inbox_events (inbox_id, session_id);
ALTER TABLE ai_inbox_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_inbox_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_timers (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 inbox_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('follow_up','periodic')),
 subject_kind text,
 subject_id uuid,
 periodic_key text,
 repeat text CHECK (repeat IS NULL OR repeat IN ('daily','weekly')),
 due_at timestamptz NOT NULL,
 payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
 status text NOT NULL CHECK (status IN ('scheduled','fired','cancelled')),
 created_at timestamptz NOT NULL,
 CHECK ((subject_kind IS NULL) = (subject_id IS NULL)),
 CHECK (subject_kind IS NULL OR subject_kind <> ''),
 CHECK (kind <> 'follow_up' OR (subject_kind IS NOT NULL AND periodic_key IS NULL AND repeat IS NULL)),
 CHECK (kind <> 'periodic' OR (periodic_key IS NOT NULL AND periodic_key <> '' AND repeat IS NOT NULL)),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, inbox_id) REFERENCES ai_inboxes (tenant_id, store_id, id)
);
CREATE UNIQUE INDEX ai_timers_follow_up_unique ON ai_timers
 (tenant_id, store_id, inbox_id, subject_kind, subject_id)
 WHERE kind = 'follow_up' AND status = 'scheduled';
CREATE UNIQUE INDEX ai_timers_periodic_unique ON ai_timers
 (tenant_id, store_id, inbox_id, periodic_key)
 WHERE kind = 'periodic';
CREATE INDEX ai_timers_due ON ai_timers (store_id, due_at) WHERE status = 'scheduled';
ALTER TABLE ai_timers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_timers
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_memory_files (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 inbox_id uuid NOT NULL,
 path text NOT NULL,
 content text NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK (path = 'instructions.md' OR path = 'index.md' OR path = 'reported.md'
     OR path ~ '^notes/[a-z0-9][a-z0-9_-]{0,63}\.md$'
     OR path ~ '^progress/[a-z0-9][a-z0-9_-]{0,63}\.md$'),
 CHECK (octet_length(content) <= 16384),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 UNIQUE (inbox_id, path),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, inbox_id) REFERENCES ai_inboxes (tenant_id, store_id, id)
);
ALTER TABLE ai_memory_files ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_memory_files
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_outbox (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 recipient_id uuid NOT NULL,
 tier text NOT NULL CHECK (tier IN ('now','daily','weekly')),
 subject_kind text NOT NULL CHECK (subject_kind <> ''),
 subject_id uuid NOT NULL,
 parent_id uuid,
 body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 200),
 status text NOT NULL CHECK (status IN ('queued','sent','withdrawn')),
 session_id uuid,
 queued_at timestamptz NOT NULL,
 sent_at timestamptz,
 read_at timestamptz,
 withdrawn_at timestamptz,
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, recipient_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, parent_id) REFERENCES ai_outbox (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id, session_id) REFERENCES ai_sessions (tenant_id, store_id, id)
);
CREATE INDEX ai_outbox_recipient ON ai_outbox (store_id, recipient_id, status, parent_id, sent_at);
CREATE INDEX ai_outbox_subject ON ai_outbox (store_id, recipient_id, subject_kind, subject_id, status);
ALTER TABLE ai_outbox ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_outbox
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_requests (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 to_staff_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('inform','ask','review')),
 subject_kind text NOT NULL CHECK (subject_kind <> ''),
 subject_id uuid NOT NULL,
 options jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(options) = 'array'),
 due_at timestamptz,
 status text NOT NULL CHECK (status IN ('open','answered','reassigned','cancelled')),
 answer text,
 answered_at timestamptz,
 previous_id uuid,
 created_at timestamptz NOT NULL,
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, to_staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, previous_id) REFERENCES ai_requests (tenant_id, store_id, id)
);
CREATE INDEX ai_requests_staff ON ai_requests (store_id, to_staff_id, status);
CREATE INDEX ai_requests_successor ON ai_requests (previous_id) WHERE previous_id IS NOT NULL;
ALTER TABLE ai_requests ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_requests
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON ai_inboxes TO salon_app;
GRANT UPDATE (lease_token, lease_until) ON ai_inboxes TO salon_app;
GRANT SELECT, INSERT ON ai_inbox_events TO salon_app;
GRANT UPDATE (status, attempts, not_before, last_error, lease_token, session_id, done_at) ON ai_inbox_events TO salon_app;
GRANT SELECT, INSERT ON ai_sessions TO salon_app;
GRANT UPDATE (status, waiting_for, messages, updated_at) ON ai_sessions TO salon_app;
GRANT SELECT, INSERT ON ai_timers TO salon_app;
GRANT UPDATE (due_at, repeat, payload, status) ON ai_timers TO salon_app;
GRANT SELECT, INSERT, DELETE ON ai_memory_files TO salon_app;
GRANT UPDATE (content, updated_at) ON ai_memory_files TO salon_app;
GRANT SELECT, INSERT ON ai_outbox TO salon_app;
GRANT UPDATE (tier, body, queued_at, status, sent_at, read_at, withdrawn_at) ON ai_outbox TO salon_app;
GRANT SELECT, INSERT ON ai_requests TO salon_app;
GRANT UPDATE (status, answer, answered_at) ON ai_requests TO salon_app;

-- +goose Down
DROP TABLE ai_requests;
DROP TABLE ai_outbox;
DROP TABLE ai_memory_files;
DROP TABLE ai_timers;
DROP TABLE ai_inbox_events;
DROP TABLE ai_sessions;
DROP TABLE ai_inboxes;
