-- name: LockClosing :exec
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/closing', 0));

-- name: LockClosingShared :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/closing', 0));

-- name: GetStoreSealing :one
SELECT * FROM store_sealings WHERE store_id=sqlc.arg(store_id);

-- name: SetStoreSealing :exec
INSERT INTO store_sealings (tenant_id,store_id,sealed_through)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(sealed_through))
ON CONFLICT (tenant_id,store_id) DO UPDATE
SET sealed_through=EXCLUDED.sealed_through,updated_at=clock_timestamp();

-- name: AddClosingEvent :one
INSERT INTO closing_events (tenant_id,store_id,business_date,previous_through,kind,reason,actor_id,at)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(business_date),sqlc.arg(previous_through),sqlc.arg(kind),sqlc.arg(reason),sqlc.arg(actor_id),sqlc.arg(at))
RETURNING *;

-- name: LatestSealEvent :one
SELECT * FROM closing_events
WHERE store_id=sqlc.arg(store_id) AND kind='seal' AND business_date=sqlc.arg(business_date)
ORDER BY at DESC, id DESC LIMIT 1;

-- name: CollectionCreatedAts :many
SELECT created_at, business_date FROM collections WHERE store_id=sqlc.arg(store_id);

-- name: CollectionEventAts :many
SELECT e.at, c.business_date,
       e.id = (SELECT e2.id FROM collection_events e2
               WHERE e2.tenant_id=e.tenant_id AND e2.collection_id=e.collection_id
               ORDER BY e2.at, e2.id LIMIT 1) AS initial
FROM collection_events e JOIN collections c ON c.tenant_id=e.tenant_id AND c.id=e.collection_id
WHERE e.store_id=sqlc.arg(store_id);

-- name: CouponRedemptionAts :many
SELECT x.created_at AS at FROM coupon_redemptions x WHERE x.store_id=sqlc.arg(store_id)
UNION ALL
SELECT x.redeemed_at AS at FROM coupon_redemptions x WHERE x.store_id=sqlc.arg(store_id) AND x.redeemed_at IS NOT NULL
UNION ALL
SELECT x.revoked_at AS at FROM coupon_redemptions x WHERE x.store_id=sqlc.arg(store_id) AND x.revoked_at IS NOT NULL;

-- name: UndoCardLedgerDates :many
SELECT l.business_date FROM import_batch_records r
JOIN card_ledger l ON l.tenant_id=r.tenant_id AND l.id=r.entity_id
WHERE r.tenant_id=sqlc.arg(tenant_id) AND r.batch_id=sqlc.arg(batch_id) AND r.kind='card_ledger';

-- name: UndoCardCountDates :many
SELECT l.business_date FROM import_batch_records r
JOIN card_count_ledger l ON l.tenant_id=r.tenant_id AND l.id=r.entity_id
WHERE r.tenant_id=sqlc.arg(tenant_id) AND r.batch_id=sqlc.arg(batch_id) AND r.kind='card_count_ledger';

-- name: UndoCardPointsDates :many
SELECT l.business_date FROM import_batch_records r
JOIN card_points_ledger l ON l.tenant_id=r.tenant_id AND l.id=r.entity_id
WHERE r.tenant_id=sqlc.arg(tenant_id) AND r.batch_id=sqlc.arg(batch_id) AND r.kind='card_points_ledger';

-- name: UndoCreatedCardTimes :many
SELECT c.opened_at FROM import_batch_records r
JOIN cards c ON c.tenant_id=r.tenant_id AND c.id=r.entity_id
WHERE r.tenant_id=sqlc.arg(tenant_id) AND r.batch_id=sqlc.arg(batch_id) AND r.kind='card' AND r.created;

-- name: UndoCreatedCustomerTimes :many
SELECT c.created_at FROM import_batch_records r
JOIN customers c ON c.tenant_id=r.tenant_id AND c.id=r.entity_id
WHERE r.tenant_id=sqlc.arg(tenant_id) AND r.batch_id=sqlc.arg(batch_id) AND r.kind='customer' AND r.created;

-- name: OpenOrdersThrough :many
SELECT id FROM orders
WHERE store_id=sqlc.arg(store_id) AND status='open' AND business_date <= sqlc.arg(through_date)
ORDER BY id;

-- name: DayHasBusiness :one
SELECT
 EXISTS (SELECT 1 FROM orders x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM checkouts x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_ledger x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_count_ledger x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_adjustments x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_upgrades x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_refunds x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_transfers x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM debts x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM debt_repayments x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM red_packets x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM order_voids x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM aftersale_refunds x JOIN aftersales a ON a.tenant_id=x.tenant_id AND a.id=x.aftersale_id WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day) AND a.status='approved')
 OR EXISTS (SELECT 1 FROM sale_payments x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM card_points_ledger x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM guest_visits x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM collections x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 OR EXISTS (SELECT 1 FROM performance_entries x WHERE x.store_id=sqlc.arg(store_id) AND x.business_date=sqlc.arg(day))
 AS has_business;

-- name: NextDayCloseVersion :one
SELECT COALESCE(max(version),0)+1 AS version FROM day_closes
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(business_date);

-- name: AddDayClose :one
INSERT INTO day_closes (tenant_id,store_id,business_date,version,system,differences,cash_counted,unionpay_actual,wechat_actual,alipay_actual,note,created_by)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(business_date),sqlc.arg(version),sqlc.arg(system),sqlc.arg(differences),sqlc.arg(cash_counted),sqlc.arg(unionpay_actual),sqlc.arg(wechat_actual),sqlc.arg(alipay_actual),sqlc.arg(note),sqlc.arg(created_by))
RETURNING *;

-- name: ListDayCloseVersions :many
SELECT * FROM day_closes
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(business_date)
ORDER BY version DESC;

-- name: ListCurrentDayCloses :many
SELECT DISTINCT ON (business_date) * FROM day_closes
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
ORDER BY business_date,version DESC;

-- name: CheckoutPaymentsByMethod :many
SELECT p.method, COALESCE(sum(p.amount),0)::text AS amount, count(*)::bigint AS count
FROM order_payments p
JOIN checkouts c ON c.tenant_id=p.tenant_id AND c.id=p.checkout_id
WHERE p.store_id=sqlc.arg(store_id) AND c.business_date=sqlc.arg(day) AND p.method <> 'card'
GROUP BY p.method;

-- name: CheckoutCardSplit :one
SELECT COALESCE(sum(-l.principal_delta),0)::text AS principal,
       COALESCE(sum(-l.bonus_delta),0)::text AS bonus
FROM order_payments p
JOIN checkouts c ON c.tenant_id=p.tenant_id AND c.id=p.checkout_id
JOIN card_ledger l ON l.tenant_id=p.tenant_id AND l.id=p.card_ledger_id
WHERE p.store_id=sqlc.arg(store_id) AND c.business_date=sqlc.arg(day) AND p.method='card';

-- name: DebtRepaymentsByMethod :many
SELECT method, COALESCE(sum(amount),0)::text AS amount, count(*)::bigint AS count
FROM debt_repayments
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(day)
GROUP BY method;

-- name: SalePaymentsByMethod :many
SELECT method, COALESCE(sum(amount),0)::text AS amount, count(*)::bigint AS count
FROM sale_payments
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(day)
GROUP BY method;

-- name: CardRefundsByMethod :many
SELECT method, COALESCE(sum(amount),0)::text AS amount, count(*)::bigint AS count
FROM card_refunds
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(day)
GROUP BY method;

-- name: AftersaleRefundsByMethod :many
SELECT r.method,COALESCE(sum(r.amount),0)::text AS amount,count(*)::bigint AS count
FROM aftersale_refunds r JOIN aftersales a ON a.tenant_id=r.tenant_id AND a.id=r.aftersale_id
WHERE r.store_id=sqlc.arg(store_id) AND r.business_date=sqlc.arg(day) AND a.status='approved'
GROUP BY r.method;

-- name: VoidPaymentsByMethod :many
SELECT p.method, COALESCE(sum(p.amount),0)::text AS amount, count(*)::bigint AS count
FROM order_voids v
JOIN order_payments p ON p.tenant_id=v.tenant_id AND p.order_id=v.order_id
WHERE v.store_id=sqlc.arg(store_id) AND v.business_date=sqlc.arg(day) AND p.method <> 'card'
GROUP BY p.method;

-- name: VoidCardSplit :one
SELECT COALESCE(sum(-l.principal_delta),0)::text AS principal,
       COALESCE(sum(-l.bonus_delta),0)::text AS bonus
FROM order_voids v
JOIN order_payments p ON p.tenant_id=v.tenant_id AND p.order_id=v.order_id
JOIN card_ledger l ON l.tenant_id=p.tenant_id AND l.id=p.card_ledger_id
WHERE v.store_id=sqlc.arg(store_id) AND v.business_date=sqlc.arg(day) AND p.method='card';

-- name: GuestVisitTotal :one
SELECT COALESCE(sum(CASE WHEN reverses_id IS NULL THEN share ELSE -share END),0)::numeric(20,4)::text AS total
FROM guest_visits
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(day);

-- name: DayCheckoutCount :one
SELECT count(*)::bigint AS count FROM checkouts
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(day);

-- name: DayVoidCount :one
SELECT count(*)::bigint AS count FROM order_voids
WHERE store_id=sqlc.arg(store_id) AND business_date=sqlc.arg(day);

-- name: UnrecordedCardSales :many
SELECT l.id, l.kind,
  (l.principal_delta + CASE WHEN l.kind='open' THEN COALESCE((SELECT sum(ci.unit_price * ci.paid_count_opened) FROM card_items ci WHERE ci.card_id=l.card_id),0) ELSE 0 END)::text AS amount
FROM card_ledger l
WHERE l.store_id=sqlc.arg(store_id) AND l.business_date=sqlc.arg(day)
  AND l.kind IN ('open','recharge','upgrade')
  AND (l.principal_delta + CASE WHEN l.kind='open' THEN COALESCE((SELECT sum(ci.unit_price * ci.paid_count_opened) FROM card_items ci WHERE ci.card_id=l.card_id),0) ELSE 0 END) > 0
  AND NOT EXISTS (SELECT 1 FROM sale_payments sp WHERE sp.tenant_id=l.tenant_id AND sp.card_ledger_id=l.id)
ORDER BY l.id;

-- name: UnrecordedRedPacketSales :many
SELECT r.id, r.price::text AS amount
FROM red_packets r
WHERE r.store_id=sqlc.arg(store_id) AND r.business_date=sqlc.arg(day)
  AND r.kind='sold' AND r.price > 0
  AND NOT EXISTS (SELECT 1 FROM sale_payments sp WHERE sp.tenant_id=r.tenant_id AND sp.red_packet_id=r.id)
ORDER BY r.id;

-- name: AddSalePayment :exec
INSERT INTO sale_payments (tenant_id,store_id,card_ledger_id,red_packet_id,method,amount,business_date,created_by)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(card_ledger_id),sqlc.arg(red_packet_id),sqlc.arg(method),sqlc.arg(amount),sqlc.arg(business_date),sqlc.arg(created_by));
