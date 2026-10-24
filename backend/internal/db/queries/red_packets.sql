-- name: CreateRedPacket :one
INSERT INTO red_packets (tenant_id,store_id,customer_id,kind,face_value,price,expires_on,business_date,issued_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: LockRedPacket :one
SELECT * FROM red_packets WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: ListCustomerRedPackets :many
SELECT * FROM red_packets WHERE store_id=$1 AND customer_id=$2 ORDER BY at DESC,id DESC;

-- name: AddRedPacketSales :exec
INSERT INTO red_packet_sales (tenant_id,store_id,red_packet_id,staff_id) VALUES ($1,$2,$3,$4);

-- name: ListRedPacketSales :many
SELECT r.red_packet_id,s.id,s.name FROM red_packet_sales r JOIN staff s ON s.tenant_id=r.tenant_id AND s.id=r.staff_id
WHERE r.store_id=$1 AND r.red_packet_id=ANY(sqlc.arg(red_packet_ids)::uuid[]) ORDER BY r.red_packet_id,s.id;

-- name: ListRedPacketPayments :many
SELECT r.id AS red_packet_id,p.order_id,p.amount FROM red_packets r
JOIN order_payments p ON p.tenant_id=r.tenant_id AND p.id=r.order_payment_id AND p.store_id=r.store_id
WHERE r.store_id=$1 AND r.id=ANY(sqlc.arg(red_packet_ids)::uuid[]);

-- name: RevokeRedPacket :one
UPDATE red_packets SET revoked_by=$2,revoked_at=clock_timestamp(),revoke_reason=$3 WHERE id=$1 RETURNING *;

-- name: UseRedPacket :exec
UPDATE red_packets SET order_payment_id=$2 WHERE id=$1;

-- name: LockMergedCustomerRedPackets :many
SELECT id FROM red_packets WHERE customer_id=$1 ORDER BY id FOR NO KEY UPDATE;

-- name: MergeCustomerRedPackets :exec
UPDATE red_packets SET customer_id=sqlc.arg(keep_id) WHERE customer_id=sqlc.arg(merged_id);
