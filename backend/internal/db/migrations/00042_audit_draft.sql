-- +goose Up
ALTER TABLE audit_log ADD COLUMN draft_id uuid,
 ADD FOREIGN KEY (tenant_id, store_id, draft_id) REFERENCES ai_drafts (tenant_id, store_id, id);
CREATE INDEX audit_log_draft ON audit_log (draft_id);
-- +goose Down
ALTER TABLE audit_log DROP COLUMN draft_id;
