-- +goose Up
ALTER TABLE performance_entries ADD COLUMN member boolean;
UPDATE performance_entries SET member = false WHERE kind <> 'sale';
ALTER TABLE performance_entries ADD CONSTRAINT performance_entries_member_check CHECK ((kind = 'sale') = (member IS NULL));

-- +goose Down
ALTER TABLE performance_entries DROP COLUMN member;
