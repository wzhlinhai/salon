-- name: AddGuestVisit :exec
INSERT INTO guest_visits (tenant_id,store_id,business_date,order_id,checkout_id,settings_snapshot_id,staff_id,share,designated,gender)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: ReverseGuestVisits :exec
INSERT INTO guest_visits (tenant_id,store_id,business_date,order_id,checkout_id,settings_snapshot_id,staff_id,share,designated,gender,void_id,reverses_id)
SELECT e.tenant_id,e.store_id,sqlc.arg(day),e.order_id,e.checkout_id,e.settings_snapshot_id,e.staff_id,e.share,e.designated,e.gender,sqlc.arg(void_id),e.id
FROM guest_visits e
WHERE e.store_id=sqlc.arg(store_id) AND e.order_id=sqlc.arg(order_id) AND e.reverses_id IS NULL
AND NOT EXISTS (SELECT 1 FROM guest_visits r WHERE r.reverses_id=e.id);

-- name: ListGuestVisits :many
SELECT e.id,e.business_date,e.order_id,e.checkout_id,e.settings_snapshot_id,e.staff_id,e.share::text AS share,e.designated,e.gender,e.reverses_id,s.name AS staff_name
FROM guest_visits e LEFT JOIN staff s ON s.id=e.staff_id
WHERE e.store_id=sqlc.arg(store_id)
AND e.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
AND (sqlc.narg(self_id)::uuid IS NULL OR e.staff_id=sqlc.narg(self_id))
AND (sqlc.narg(staff_id)::uuid IS NULL OR e.staff_id=sqlc.narg(staff_id))
AND (sqlc.narg(before_id)::uuid IS NULL OR e.id < sqlc.narg(before_id))
ORDER BY e.id DESC LIMIT sqlc.arg(row_limit);

-- name: GuestVisitTotals :one
SELECT COALESCE(sum(value),0)::numeric(20,4)::text AS total,
COALESCE(sum(value) FILTER (WHERE designated),0)::numeric(20,4)::text AS designated,
COALESCE(sum(value) FILTER (WHERE NOT designated),0)::numeric(20,4)::text AS not_designated,
COALESCE(sum(value) FILTER (WHERE gender='male'),0)::numeric(20,4)::text AS male,
COALESCE(sum(value) FILTER (WHERE gender='female'),0)::numeric(20,4)::text AS female
FROM (
SELECT e.designated,e.gender,CASE WHEN e.reverses_id IS NULL THEN e.share ELSE -e.share END AS value
FROM guest_visits e
WHERE e.store_id=sqlc.arg(store_id)
AND e.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
AND (sqlc.narg(self_id)::uuid IS NULL OR e.staff_id=sqlc.narg(self_id))
AND (sqlc.narg(staff_id)::uuid IS NULL OR e.staff_id=sqlc.narg(staff_id))
) filtered;
