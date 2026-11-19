-- +goose Up
ALTER TABLE ai_drafts ADD COLUMN version integer NOT NULL DEFAULT 1;
GRANT UPDATE (version) ON ai_drafts TO salon_app;

-- +goose Down
ALTER TABLE ai_drafts DROP COLUMN version;
