-- name: EffectiveSettings :many
-- 每项设置在营业日 @business_date 生效的那一行。
SELECT DISTINCT ON (key) key, value, is_default, effective_from
FROM setting_changes
WHERE store_id = @store_id AND effective_from <= @business_date::date
ORDER BY key, effective_from DESC, changed_at DESC, id DESC;

-- name: UpcomingSettingChanges :many
-- 营业日 @after 之后才生效的修改，每个生效日期只取最后一次修改。
SELECT DISTINCT ON (key, effective_from) key, value, is_default, effective_from
FROM setting_changes
WHERE store_id = @store_id AND effective_from > @after::date
ORDER BY key, effective_from, changed_at DESC, id DESC;

-- name: AddSettingChange :one
INSERT INTO setting_changes (tenant_id, store_id, key, value, is_default, old_value, old_is_default, effective_from, changed_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListSettingChanges :many
SELECT sqlc.embed(c), s.name AS changed_by_name
FROM setting_changes c JOIN staff s ON s.id = c.changed_by
WHERE c.store_id = @store_id AND (sqlc.narg(key)::text IS NULL OR c.key = sqlc.narg(key))
    AND (sqlc.narg(before_id)::uuid IS NULL OR (c.changed_at, c.id) < (SELECT b.changed_at, b.id FROM setting_changes b WHERE b.id = sqlc.narg(before_id) AND b.store_id = @store_id))
ORDER BY c.changed_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: SaveSettingsSnapshot :one
-- 内容相同的快照已存在时返回已有的那一行。并发保存同样内容时，后到的语句看不到先到的行，返回 0 行，由调用方重试。
WITH saved AS (
    INSERT INTO settings_snapshots (tenant_id, store_id, rules, digest)
    VALUES (@tenant_id, @store_id, @rules::jsonb, md5(CAST(sqlc.arg(rules)::jsonb AS text)))
    ON CONFLICT (store_id, digest) DO NOTHING
    RETURNING id
)
SELECT id FROM saved
UNION ALL
SELECT id FROM settings_snapshots WHERE store_id = @store_id AND digest = md5(CAST(sqlc.arg(rules)::jsonb AS text))
LIMIT 1;

-- name: GetSettingsSnapshot :one
SELECT * FROM settings_snapshots WHERE id = $1 AND store_id = $2;

-- name: LockSettingKey :exec
-- 在事务结束前锁住门店的一项设置，同一项设置的修改排队进行。
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/' || sqlc.arg(key)::text, 0));
