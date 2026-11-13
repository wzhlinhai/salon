-- +goose Up
ALTER TABLE import_batch_rows DROP CONSTRAINT import_batch_rows_source_key_check;
ALTER TABLE import_batch_rows ADD CONSTRAINT import_batch_rows_source_key_check
    CHECK (btrim(source_key) <> '' OR error IS NOT NULL);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM import_batch_rows WHERE btrim(source_key) = '') THEN
        RAISE EXCEPTION '无法恢复来源编号约束：存在空编号的导入错误行';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE import_batch_rows DROP CONSTRAINT import_batch_rows_source_key_check;
ALTER TABLE import_batch_rows ADD CONSTRAINT import_batch_rows_source_key_check CHECK (btrim(source_key) <> '');
