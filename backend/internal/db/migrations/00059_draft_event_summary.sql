-- +goose Up
GRANT UPDATE (summary) ON ai_drafts TO salon_app;
ALTER TABLE ai_draft_events ADD COLUMN before_summary text;

-- +goose Down
ALTER TABLE ai_draft_events DROP COLUMN before_summary;
REVOKE UPDATE (summary) ON ai_drafts FROM salon_app;
