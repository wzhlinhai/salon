-- name: CreateCustomerSource :one
INSERT INTO customer_sources (tenant_id, store_id, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: LockCustomerSource :one
SELECT * FROM customer_sources WHERE id = $1 FOR UPDATE;

-- name: ListCustomerSources :many
SELECT * FROM customer_sources WHERE store_id = $1 ORDER BY name, id;

-- name: UpdateCustomerSource :one
UPDATE customer_sources SET name = $2, active = $3 WHERE id = $1
RETURNING *;

-- name: CreateCustomer :one
INSERT INTO customers (tenant_id, store_id, name, phone, gender, source_id, note, created_by, created_by_customer)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (tenant_id, phone) WHERE phone IS NOT NULL AND merged_into IS NULL DO NOTHING
RETURNING *;

-- name: CreateCustomerAsCustomer :one
-- 顾客本人通过小程序建档：新建时还不知道自己的 ID，先分配 ID 再把同一 ID 记作创建人。
INSERT INTO customers (id, tenant_id, store_id, name, phone, created_by_customer)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, phone) WHERE phone IS NOT NULL AND merged_into IS NULL DO NOTHING
RETURNING *;

-- name: FindCustomerByPhone :one
SELECT * FROM customers WHERE phone = $1 AND merged_into IS NULL;

-- name: FindStoreCustomerByPhone :one
SELECT * FROM customers
WHERE store_id = $1 AND phone = $2
ORDER BY (merged_into IS NULL) DESC, created_at DESC, id
LIMIT 1;

-- name: LockCustomerPhone :exec
SELECT pg_advisory_xact_lock(hashtextextended(current_setting('app.tenant_id') || sqlc.arg(phone)::text, 0));

-- name: GetCustomer :one
SELECT * FROM customers WHERE id = $1;

-- name: LockCustomer :one
SELECT * FROM customers WHERE id = $1 FOR NO KEY UPDATE;

-- name: SaveCustomer :exec
UPDATE customers
SET name = $2, phone = $3, gender = $4, source_id = $5,
    responsible_staff_id = $6, note = $7, merged_into = $8
WHERE id = $1;

-- name: SearchCustomers :many
SELECT * FROM customers
WHERE merged_into IS NULL
    AND (sqlc.arg(include_unidentified)::boolean OR phone IS NOT NULL)
    AND (sqlc.narg(responsible_staff_id)::uuid IS NULL OR responsible_staff_id = sqlc.narg(responsible_staff_id))
    AND (sqlc.narg(self_id)::uuid IS NULL OR responsible_staff_id = sqlc.narg(self_id))
    AND (sqlc.arg(query)::text = '' OR
        CASE
            WHEN sqlc.arg(query)::text ~ '^[0-9]{11}$' THEN phone = sqlc.arg(query)
            WHEN sqlc.arg(query)::text ~ '^[0-9]{4}$' THEN right(phone, 4) = sqlc.arg(query)
            ELSE strpos(lower(coalesce(name, '')), lower(sqlc.arg(query))) > 0
        END)
ORDER BY created_at DESC, id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: LockResponsibleCustomers :many
SELECT * FROM customers
WHERE responsible_staff_id = $1 AND merged_into IS NULL
ORDER BY id FOR NO KEY UPDATE;

-- name: ListCustomerViewSources :many
SELECT id, name FROM customer_sources WHERE id = ANY(@ids::uuid[]);

-- name: ListCustomerViewStaff :many
SELECT id, name FROM staff WHERE id = ANY(@ids::uuid[]);

-- name: ListCustomerViewSafetyLabels :many
SELECT * FROM customer_safety_labels
WHERE customer_id = ANY(@ids::uuid[]) AND revoked_at IS NULL
ORDER BY observed_on DESC, created_at DESC;

-- name: AddSafetyLabel :exec
INSERT INTO customer_safety_labels (tenant_id, store_id, customer_id, kind, source, note, observed_on, recorded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: RevokeSafetyLabel :execrows
UPDATE customer_safety_labels
SET revoked_at = clock_timestamp(), revoked_by = $3
WHERE id = $1 AND customer_id = $2 AND revoked_at IS NULL;

-- name: MergeCustomerSafetyLabels :exec
UPDATE customer_safety_labels SET customer_id = sqlc.arg(keep_id) WHERE customer_id = sqlc.arg(merged_id);

-- name: MergeCustomerAppointments :exec
UPDATE appointments SET customer_id = sqlc.arg(keep_id)::uuid WHERE customer_id = sqlc.arg(merged_id)::uuid;

-- name: ShareCustomerSource :one
-- 共享锁阻止并发修改来源，同时允许其他顾客检查来源状态。
SELECT * FROM customer_sources WHERE id = $1 FOR SHARE;

-- name: ShareCustomer :one
SELECT * FROM customers WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: GetCustomers :many
SELECT * FROM customers WHERE store_id=sqlc.arg(store_id) AND id=ANY(sqlc.arg(ids)::uuid[]);

-- name: LockCardOpeningCustomer :one
SELECT * FROM customers WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: MergeCustomerLogins :exec
UPDATE customer_logins SET customer_id=sqlc.arg(keep_id) WHERE customer_id=sqlc.arg(merged_id);

-- name: LockCustomerLogin :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(store_id)::text || sqlc.arg(openid)::text, 0));

-- name: GetCustomerLoginByOpenID :one
SELECT * FROM customer_logins WHERE store_id=$1 AND openid=$2;

-- name: GetCustomerLogin :one
SELECT * FROM customer_logins WHERE id=$1;

-- name: CreateCustomerLogin :one
INSERT INTO customer_logins (tenant_id, store_id, openid, unionid, customer_id, last_login_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (store_id, openid) DO UPDATE
SET unionid=COALESCE(EXCLUDED.unionid, customer_logins.unionid), last_login_at=EXCLUDED.last_login_at
RETURNING *;

-- name: TouchCustomerLogin :exec
UPDATE customer_logins
SET unionid=COALESCE($3, unionid), last_login_at=$4
WHERE store_id=$1 AND id=$2;

-- name: MoveCustomerLoginCustomer :exec
UPDATE customer_logins SET customer_id=$3 WHERE store_id=$1 AND id=$2;
