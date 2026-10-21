-- +goose Up
ALTER TABLE staff
    ADD COLUMN token_version integer NOT NULL DEFAULT 0,
    ADD COLUMN failed_logins integer NOT NULL DEFAULT 0,
    ADD COLUMN locked_until timestamptz;

-- +goose Down
ALTER TABLE staff
    DROP COLUMN locked_until,
    DROP COLUMN failed_logins,
    DROP COLUMN token_version;
