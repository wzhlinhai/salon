-- name: LockImportStore :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(store_id)::text || '/data_import', 0));

-- name: CreateImportBatch :one
INSERT INTO import_batches (tenant_id, store_id, file_name, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id, status, file_name, created_at;

-- name: SetImportBatchFileTotals :exec
UPDATE import_batches
SET declared_customers=$2, declared_stored_cards=$3, declared_principal=$4, declared_bonus=$5, declared_count_cards=$6,
    file_customers=$7, file_stored_cards=$8, file_principal=$9, file_bonus=$10, file_count_cards=$11
WHERE id=$1;

-- name: LockImportBatch :one
SELECT * FROM import_batches WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: GetImportBatch :one
SELECT id, status, file_name, created_by, created_at, committed_by, committed_at, undone_by, undone_at,
       declared_customers, declared_stored_cards, declared_principal::text, declared_bonus::text, declared_count_cards,
       file_customers, file_stored_cards, file_principal::text, file_bonus::text, file_count_cards,
       written_customers, written_stored_cards, written_principal::text, written_bonus::text, written_count_cards
FROM import_batches WHERE store_id=$1 AND id=$2;

-- name: ListImportBatches :many
SELECT b.id, b.status, b.file_name, b.created_by, b.created_at, b.committed_by, b.committed_at, b.undone_by, b.undone_at,
       b.declared_customers, b.declared_stored_cards, b.declared_principal::text, b.declared_bonus::text, b.declared_count_cards,
       b.file_customers, b.file_stored_cards, b.file_principal::text, b.file_bonus::text, b.file_count_cards,
       b.written_customers, b.written_stored_cards, b.written_principal::text, b.written_bonus::text, b.written_count_cards,
       (SELECT count(*) FROM import_batch_rows r WHERE r.batch_id=b.id AND r.error IS NOT NULL)::int AS error_rows,
       (SELECT count(*) FROM import_batch_rows r WHERE r.batch_id=b.id)::int AS row_count
FROM import_batches b WHERE b.store_id=$1 ORDER BY b.created_at DESC, b.id DESC;

-- name: MarkImportBatchCommitted :exec
UPDATE import_batches
SET status='committed', committed_by=$2, committed_at=$3,
    written_customers=$4, written_stored_cards=$5, written_principal=$6, written_bonus=$7, written_count_cards=$8
WHERE id=$1;

-- name: MarkImportBatchUndone :exec
UPDATE import_batches SET status='undone', undone_by=$2, undone_at=$3 WHERE id=$1;

-- name: AddImportBatchRow :one
INSERT INTO import_batch_rows (tenant_id, store_id, batch_id, sheet, row_number, kind, source_key, customer_source_key, name, phone, gender,
    card_type_name, card_type_id, item_name, item_id, principal, bonus, points, paid_count, gift_count, expires_on, error, action, entity_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
RETURNING id;

-- name: UpdateImportBatchRow :exec
UPDATE import_batch_rows SET error=$4, action=$5, entity_id=$6
WHERE store_id=$1 AND batch_id=$2 AND id=$3;

-- name: ListImportBatchRows :many
SELECT * FROM import_batch_rows WHERE store_id=$1 AND batch_id=$2
ORDER BY CASE sheet WHEN '顾客' THEN 1 WHEN '储值卡' THEN 2 ELSE 3 END, row_number;

-- name: AddImportBatchRecord :exec
INSERT INTO import_batch_records (tenant_id, store_id, batch_id, kind, entity_id, source_key, created)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT DO NOTHING;

-- name: CommittedImportedCardKeys :many
SELECT r.source_key FROM import_batch_records r JOIN import_batches b ON b.tenant_id=r.tenant_id AND b.id=r.batch_id
WHERE r.store_id=$1 AND r.kind='card' AND b.status='committed' AND r.source_key = ANY(sqlc.arg(keys)::text[]);

-- name: UndoImportBatch :one
SELECT array_to_json(undo_import_batch(sqlc.arg(batch_id)::uuid, sqlc.arg(store_id)::uuid))::text AS refs;

-- name: FindCustomersByPhones :many
-- 手机号在租户内唯一；同一租户的其他门店也要按租户范围匹配。
SELECT * FROM customers
WHERE merged_into IS NULL AND phone = ANY(sqlc.arg(phones)::text[]);

-- name: ExportCustomers :many
SELECT id, name, phone, gender, created_at FROM customers WHERE store_id=$1 ORDER BY created_at, id;

-- name: ExportStoredCards :many
SELECT c.id, c.customer_id, t.name AS card_type_name, l.principal_after::text AS principal, l.bonus_after::text AS bonus,
       c.expires_on, COALESCE(p.after,0)::int AS points
FROM cards c JOIN card_types t ON t.id=c.card_type_id
JOIN LATERAL (SELECT principal_after,bonus_after FROM card_ledger WHERE card_id=c.id ORDER BY at DESC,id DESC LIMIT 1) l ON true
LEFT JOIN LATERAL (SELECT after FROM card_points_ledger WHERE card_id=c.id ORDER BY at DESC,id DESC LIMIT 1) p ON true
WHERE c.store_id=$1 AND t.kind='stored_value'
ORDER BY c.opened_at, c.id;

-- name: ExportCountCards :many
SELECT c.id, c.customer_id, t.name AS card_type_name, i.name AS item_name,
       cl.paid_after AS paid_count, cl.gift_after AS gift_count, c.expires_on
FROM cards c JOIN card_types t ON t.id=c.card_type_id
JOIN card_items ci ON ci.card_id=c.id JOIN items i ON i.id=ci.item_id
JOIN LATERAL (SELECT paid_after,gift_after FROM card_count_ledger WHERE card_id=c.id AND item_id=ci.item_id ORDER BY at DESC,id DESC LIMIT 1) cl ON true
WHERE c.store_id=$1 AND t.kind IN ('count','package')
ORDER BY c.opened_at, c.id, ci.item_id;

-- name: ExportCardLedger :many
SELECT l.id, l.card_id, l.kind, l.principal_before::text AS principal_before, l.principal_delta::text AS principal_delta,
       l.principal_after::text AS principal_after, l.bonus_before::text AS bonus_before, l.bonus_delta::text AS bonus_delta,
       l.bonus_after::text AS bonus_after, l.business_date, l.at, s.name AS actor_name
FROM card_ledger l JOIN staff s ON s.id=l.actor_id
WHERE l.store_id=$1 ORDER BY l.at, l.id;

-- name: ExportOrders :many
SELECT o.id, o.customer_id, o.gender, o.designated, o.status, o.business_date, o.opened_at, s.name AS opened_by_name,
       o.checkout_id, o.merged_into
FROM orders o JOIN staff s ON s.id=o.opened_by
WHERE o.store_id=$1 ORDER BY o.opened_at, o.id;

-- name: ExportOrderItems :many
SELECT i.id, i.order_id, i.item_name, i.item_kind, i.price::text AS price, i.discount::text AS discount,
       i.price_source, i.added_at, i.removed_at
FROM order_items i WHERE i.store_id=$1 ORDER BY i.order_id, i.added_at, i.id;

-- name: ExportOrderPayments :many
SELECT p.id, p.checkout_id, p.order_id, p.method, p.amount::text AS amount,
       p.card_ledger_id, p.card_count_ledger_id, p.red_packet_id
FROM order_payments p WHERE p.store_id=$1 ORDER BY p.id;

-- name: ExportStaff :many
SELECT id, name, number, active, login_account, deactivated_at, created_at FROM staff WHERE store_id=$1 ORDER BY number, id;

-- name: ExportItems :many
SELECT id, name, kind, price::text AS price, discountable, COALESCE(labor_fee_amount,0)::text AS labor_fee_amount_text,
       COALESCE(labor_fee_percent,0)::text AS labor_fee_percent_text, active
FROM items WHERE store_id=$1 ORDER BY name, id;

-- name: ExportCardTypes :many
SELECT id, name, kind, COALESCE(discount_percent,0)::text AS discount_percent_text, validity_kind, validity_date, validity_days, active
FROM card_types WHERE store_id=$1 ORDER BY name, id;

-- name: AddImportCardPoints :one
INSERT INTO card_points_ledger (tenant_id, store_id, card_id, kind, before, delta, after, card_ledger_id, business_date, actor_id)
VALUES ($1,$2,$3,'import',0,$4,$5,$6,$7,$8) RETURNING id;

-- name: ExportCardTypeItems :many
SELECT ci.card_type_id, i.name AS item_name, ci.paid_count, ci.gift_count, ci.unit_price::text AS unit_price
FROM card_type_items ci JOIN items i ON i.id=ci.item_id
WHERE ci.store_id=$1 ORDER BY ci.card_type_id, ci.item_id;
