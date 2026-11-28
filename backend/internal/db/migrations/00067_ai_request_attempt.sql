-- +goose Up
-- 转交时记录这条交办已经被提醒过几次（ADR-0015）。第一次到点再提醒同一人，之后改交。
ALTER TABLE ai_requests ADD COLUMN attempt smallint NOT NULL DEFAULT 0 CHECK (attempt >= 0);
GRANT UPDATE (attempt) ON ai_requests TO salon_app;

-- +goose Down
REVOKE UPDATE (attempt) ON ai_requests FROM salon_app;
ALTER TABLE ai_requests DROP COLUMN attempt;
