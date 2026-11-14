-- name: BusinessCheckoutPayments :many
SELECT c.business_date, p.method, COALESCE(sum(p.amount),0)::text AS amount
FROM order_payments p
JOIN checkouts c ON c.tenant_id=p.tenant_id AND c.id=p.checkout_id
WHERE p.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND p.method <> 'card'
GROUP BY c.business_date,p.method;

-- name: BusinessCheckoutCardSplit :many
SELECT c.business_date,
       COALESCE(sum(-l.principal_delta),0)::text AS principal,
       COALESCE(sum(-l.bonus_delta),0)::text AS bonus
FROM order_payments p
JOIN checkouts c ON c.tenant_id=p.tenant_id AND c.id=p.checkout_id
JOIN card_ledger l ON l.tenant_id=p.tenant_id AND l.id=p.card_ledger_id
WHERE p.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND p.method='card'
GROUP BY c.business_date;

-- name: BusinessDebtRepayments :many
SELECT business_date, method, COALESCE(sum(amount),0)::text AS amount
FROM debt_repayments
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY business_date,method;

-- name: BusinessSalePayments :many
SELECT business_date, method, COALESCE(sum(amount),0)::text AS amount
FROM sale_payments
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY business_date,method;

-- name: BusinessNewDebts :many
SELECT d.business_date, COALESCE(sum(d.amount),0)::text AS amount
FROM debts d
WHERE d.store_id=sqlc.arg(store_id) AND d.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY d.business_date
UNION ALL
SELECT v.business_date, COALESCE(-sum(d.amount),0)::text AS amount
FROM order_voids v
JOIN debts d ON d.tenant_id=v.tenant_id AND d.order_id=v.order_id
WHERE v.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY v.business_date;

-- name: BusinessCardRefunds :many
SELECT business_date, method, COALESCE(sum(amount),0)::text AS amount
FROM card_refunds
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY business_date,method;

-- name: BusinessAftersaleRefunds :many
SELECT r.business_date, r.method, COALESCE(sum(r.amount),0)::text AS amount
FROM aftersale_refunds r JOIN aftersales a ON a.tenant_id=r.tenant_id AND a.id=r.aftersale_id
WHERE r.store_id=sqlc.arg(store_id) AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND a.status='approved'
GROUP BY r.business_date,r.method;

-- name: BusinessVoidPayments :many
SELECT v.business_date, p.method, COALESCE(sum(p.amount),0)::text AS amount
FROM order_voids v
JOIN order_payments p ON p.tenant_id=v.tenant_id AND p.order_id=v.order_id
WHERE v.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND p.method <> 'card'
GROUP BY v.business_date,p.method;

-- name: BusinessVoidCardSplit :many
SELECT v.business_date,
       COALESCE(sum(-l.principal_delta),0)::text AS principal,
       COALESCE(sum(-l.bonus_delta),0)::text AS bonus
FROM order_voids v
JOIN order_payments p ON p.tenant_id=v.tenant_id AND p.order_id=v.order_id
JOIN card_ledger l ON l.tenant_id=p.tenant_id AND l.id=p.card_ledger_id
WHERE v.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND p.method='card'
GROUP BY v.business_date;

-- name: BusinessServiceRevenue :many
SELECT d.business_date, COALESCE(sum(d.amount),0)::text AS amount
FROM (
  SELECT c.business_date, p.amount
  FROM order_payments p JOIN checkouts c ON c.tenant_id=p.tenant_id AND c.id=p.checkout_id
  WHERE p.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  UNION ALL
  SELECT v.business_date, -p.amount
  FROM order_voids v JOIN order_payments p ON p.tenant_id=v.tenant_id AND p.order_id=v.order_id
  WHERE v.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
) d
GROUP BY d.business_date;

-- name: BusinessGuestVisits :many
SELECT x.business_date,
       COALESCE(sum(x.value),0)::numeric(20,4)::text AS total,
       COALESCE(sum(x.value) FILTER (WHERE x.designated),0)::numeric(20,4)::text AS designated,
       COALESCE(sum(x.value) FILTER (WHERE NOT x.designated),0)::numeric(20,4)::text AS not_designated,
       COALESCE(sum(x.value) FILTER (WHERE x.gender='male'),0)::numeric(20,4)::text AS male,
       COALESCE(sum(x.value) FILTER (WHERE x.gender='female'),0)::numeric(20,4)::text AS female
FROM (
  SELECT g.business_date,
         CASE WHEN g.reverses_id IS NULL THEN g.share ELSE -g.share END AS value,
         g.designated,g.gender
  FROM guest_visits g
  WHERE g.store_id=sqlc.arg(store_id) AND g.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
) x
GROUP BY x.business_date;

-- name: BusinessCheckoutCounts :many
SELECT business_date, count(*)::bigint AS count
FROM checkouts
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY business_date;

-- name: BusinessVoidCounts :many
SELECT business_date, count(*)::bigint AS count
FROM order_voids
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
GROUP BY business_date;

-- name: StaffPerformance :many
SELECT staff_id, kind,
       COALESCE(sum(CASE WHEN reverses_id IS NULL THEN amount ELSE -amount END),0)::text AS amount,
       COALESCE(sum(CASE WHEN reverses_id IS NULL THEN amount ELSE -amount END) FILTER (WHERE kind='item' AND method IN ('cash','unionpay','wechat','alipay','debt')),0)::text AS cash_performance,
       COALESCE(sum(CASE WHEN reverses_id IS NULL THEN amount ELSE -amount END) FILTER (WHERE kind='item' AND method IN ('card_principal','card_bonus')),0)::text AS card_performance
FROM performance_entries
WHERE store_id=sqlc.arg(store_id) AND business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  AND (sqlc.narg(staff_id)::uuid IS NULL OR staff_id=sqlc.narg(staff_id))
GROUP BY staff_id,kind;

-- name: StaffGuestVisits :many
SELECT staff_id,
       COALESCE(sum(value),0)::numeric(20,4)::text AS total,
       COALESCE(sum(value) FILTER (WHERE designated),0)::numeric(20,4)::text AS designated,
       COALESCE(sum(value) FILTER (WHERE NOT designated),0)::numeric(20,4)::text AS not_designated,
       COALESCE(sum(value) FILTER (WHERE gender='male'),0)::numeric(20,4)::text AS male,
       COALESCE(sum(value) FILTER (WHERE gender='female'),0)::numeric(20,4)::text AS female
FROM (
  SELECT g.staff_id,g.designated,g.gender,
         CASE WHEN g.reverses_id IS NULL THEN g.share ELSE -g.share END AS value
  FROM guest_visits g
  WHERE g.store_id=sqlc.arg(store_id) AND g.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND (sqlc.narg(staff_id)::uuid IS NULL OR g.staff_id=sqlc.narg(staff_id))
) x
GROUP BY staff_id;

-- name: StaffItemLines :many
WITH facts AS (
  SELECT p.staff_id, i.id AS item_id, 1 AS sign
  FROM order_items i
  JOIN orders o ON o.tenant_id=i.tenant_id AND o.id=i.order_id
  JOIN checkouts c ON c.tenant_id=o.tenant_id AND c.id=o.checkout_id
  JOIN order_item_positions p ON p.tenant_id=i.tenant_id AND p.order_item_id=i.id
  WHERE i.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND i.removed_at IS NULL AND p.staff_id IS NOT NULL
    AND (sqlc.narg(staff_id)::uuid IS NULL OR p.staff_id=sqlc.narg(staff_id))
  UNION ALL
  SELECT p.staff_id, i.id, -1
  FROM order_items i
  JOIN orders o ON o.tenant_id=i.tenant_id AND o.id=i.order_id
  JOIN order_voids v ON v.tenant_id=o.tenant_id AND v.order_id=o.id
  JOIN order_item_positions p ON p.tenant_id=i.tenant_id AND p.order_item_id=i.id
  WHERE i.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND i.removed_at IS NULL AND p.staff_id IS NOT NULL
    AND (sqlc.narg(staff_id)::uuid IS NULL OR p.staff_id=sqlc.narg(staff_id))
)
SELECT staff_id, COALESCE(sum(sign),0)::bigint AS lines
FROM (SELECT DISTINCT staff_id,item_id,sign FROM facts) distinct_facts
GROUP BY staff_id;

-- name: ItemReport :many
WITH line_facts AS (
  SELECT i.item_id, 1 AS sign, i.price, i.discount, o.designated
  FROM order_items i
  JOIN orders o ON o.tenant_id=i.tenant_id AND o.id=i.order_id
  JOIN checkouts c ON c.tenant_id=o.tenant_id AND c.id=o.checkout_id
  WHERE i.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND i.removed_at IS NULL
  UNION ALL
  SELECT i.item_id, -1, i.price, i.discount, o.designated
  FROM order_items i
  JOIN orders o ON o.tenant_id=i.tenant_id AND o.id=i.order_id
  JOIN order_voids v ON v.tenant_id=o.tenant_id AND v.order_id=o.id
  WHERE i.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND i.removed_at IS NULL
),
line_totals AS (
  SELECT item_id,
         COALESCE(sum(sign),0)::bigint AS times,
         COALESCE(sum(sign*price),0)::text AS line_price,
         COALESCE(sum(sign*(price-discount)),0)::text AS paid,
         COALESCE(sum(CASE WHEN designated THEN sign ELSE 0 END),0)::bigint AS designated
  FROM line_facts GROUP BY item_id
),
perf AS (
  SELECT oi.item_id,
         COALESCE(sum(CASE WHEN e.reverses_id IS NULL THEN e.amount ELSE -e.amount END),0)::text AS performance,
         COALESCE(sum(CASE WHEN e.reverses_id IS NULL THEN e.paid_amount ELSE -e.paid_amount END) FILTER (WHERE e.method IN ('card_principal','card_bonus')),0)::text AS card_paid,
         COALESCE(sum(CASE WHEN e.reverses_id IS NULL THEN e.paid_amount ELSE -e.paid_amount END) FILTER (WHERE e.method='groupbuy'),0)::text AS groupbuy_paid
  FROM performance_entries e
  JOIN order_items oi ON oi.tenant_id=e.tenant_id AND oi.id=e.order_item_id
  WHERE e.store_id=sqlc.arg(store_id) AND e.kind='item' AND e.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  GROUP BY oi.item_id
),
count_facts AS (
  SELECT oi.item_id, p.amount, 1 AS sign
  FROM order_payments p
  JOIN order_items oi ON oi.tenant_id=p.tenant_id AND oi.id=p.order_item_id
  JOIN orders o ON o.tenant_id=p.tenant_id AND o.id=p.order_id
  JOIN checkouts c ON c.tenant_id=o.tenant_id AND c.id=o.checkout_id
  WHERE p.store_id=sqlc.arg(store_id) AND p.method='count' AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  UNION ALL
  SELECT oi.item_id, p.amount, -1
  FROM order_payments p
  JOIN order_items oi ON oi.tenant_id=p.tenant_id AND oi.id=p.order_item_id
  JOIN orders o ON o.tenant_id=p.tenant_id AND o.id=p.order_id
  JOIN order_voids v ON v.tenant_id=o.tenant_id AND v.order_id=o.id
  WHERE p.store_id=sqlc.arg(store_id) AND p.method='count' AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
),
count_totals AS (
  SELECT item_id, COALESCE(sum(sign*amount),0)::text AS count_paid FROM count_facts GROUP BY item_id
)
SELECT i.id AS item_id, i.name AS item_name, i.kind AS item_kind,
       lt.times, lt.line_price, lt.paid, lt.designated,
       COALESCE(p.card_paid,'0')::text AS card_paid,
       COALESCE(ct.count_paid,'0')::text AS count_paid,
       COALESCE(p.groupbuy_paid,'0')::text AS groupbuy_paid,
       COALESCE(p.performance,'0')::text AS performance
FROM line_totals lt
JOIN items i ON i.id=lt.item_id
LEFT JOIN perf p ON p.item_id=lt.item_id
LEFT JOIN count_totals ct ON ct.item_id=lt.item_id
WHERE i.store_id=sqlc.arg(store_id)
ORDER BY lt.paid::numeric DESC, i.name, i.id;

-- name: ReportCardTypes :many
SELECT id,name FROM card_types
WHERE store_id=sqlc.arg(store_id)
ORDER BY name,id;

-- name: ReportCardSales :many
WITH first_types AS (
  SELECT DISTINCT ON (u.card_id) u.card_id,u.type_before
  FROM card_upgrades u
  ORDER BY u.card_id,u.at,u.id
),
typed AS (
  SELECT l.kind,l.principal_delta,l.bonus_delta,
         COALESCE(u.type_after,ft.type_before,c.card_type_id) AS card_type_id,
         row_number() OVER (PARTITION BY l.id ORDER BY u.at DESC,u.id DESC) AS rn
  FROM card_ledger l
  JOIN cards c ON c.tenant_id=l.tenant_id AND c.id=l.card_id
  LEFT JOIN card_upgrades u ON u.tenant_id=l.tenant_id AND u.card_id=l.card_id AND u.at <= l.at
  LEFT JOIN first_types ft ON ft.card_id=l.card_id
  WHERE l.store_id=sqlc.arg(store_id) AND l.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND l.kind IN ('open','recharge','upgrade')
)
SELECT card_type_id,
       count(*) FILTER (WHERE kind='open')::bigint AS open_count,
       COALESCE(sum(principal_delta) FILTER (WHERE kind='open'),0)::text AS open_principal,
       COALESCE(sum(bonus_delta) FILTER (WHERE kind='open'),0)::text AS open_bonus,
       COALESCE(sum(principal_delta) FILTER (WHERE kind='recharge'),0)::text AS recharge_principal,
       COALESCE(sum(bonus_delta) FILTER (WHERE kind='recharge'),0)::text AS recharge_bonus,
       COALESCE(sum(principal_delta) FILTER (WHERE kind='upgrade'),0)::text AS upgrade
FROM typed WHERE rn=1
GROUP BY card_type_id;

-- name: ReportCardConsumption :many
WITH first_types AS (
  SELECT DISTINCT ON (u.card_id) u.card_id,u.type_before
  FROM card_upgrades u
  ORDER BY u.card_id,u.at,u.id
),
typed AS (
  SELECT l.principal_delta,l.bonus_delta,
         COALESCE(u.type_after,ft.type_before,c.card_type_id) AS card_type_id,
         row_number() OVER (PARTITION BY l.id ORDER BY u.at DESC,u.id DESC) AS rn
  FROM card_ledger l
  JOIN cards c ON c.tenant_id=l.tenant_id AND c.id=l.card_id
  LEFT JOIN card_upgrades u ON u.tenant_id=l.tenant_id AND u.card_id=l.card_id AND u.at <= l.at
  LEFT JOIN first_types ft ON ft.card_id=l.card_id
  WHERE l.store_id=sqlc.arg(store_id) AND l.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND l.kind IN ('consume','void','aftersale_refund')
)
SELECT card_type_id,
       COALESCE(sum(-principal_delta),0)::text AS consumed_principal,
       COALESCE(sum(-bonus_delta),0)::text AS consumed_bonus
FROM typed WHERE rn=1
GROUP BY card_type_id;

-- name: ReportCardCounts :many
WITH first_types AS (
  SELECT DISTINCT ON (u.card_id) u.card_id,u.type_before
  FROM card_upgrades u
  ORDER BY u.card_id,u.at,u.id
),
typed AS (
  SELECT l.kind,l.id AS ledger_id,l.tenant_id,l.reverses_id,
         COALESCE(u.type_after,ft.type_before,c.card_type_id) AS card_type_id,
         row_number() OVER (PARTITION BY l.id ORDER BY u.at DESC,u.id DESC) AS rn
  FROM card_count_ledger l
  JOIN cards c ON c.tenant_id=l.tenant_id AND c.id=l.card_id
  LEFT JOIN card_upgrades u ON u.tenant_id=l.tenant_id AND u.card_id=l.card_id AND u.at <= l.at
  LEFT JOIN first_types ft ON ft.card_id=l.card_id
  WHERE l.store_id=sqlc.arg(store_id) AND l.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND l.kind IN ('consume','void')
)
SELECT t.card_type_id,
       COALESCE(sum(CASE WHEN t.kind='consume' THEN 1 WHEN t.kind='void' THEN -1 ELSE 0 END),0)::bigint AS count_used,
       COALESCE(sum(CASE WHEN t.kind='consume' THEN COALESCE(p.amount,0) WHEN t.kind='void' THEN -COALESCE(p.amount,0) ELSE 0 END),0)::text AS count_amount
FROM typed t
LEFT JOIN card_count_ledger orig ON orig.tenant_id=t.tenant_id AND orig.id=t.reverses_id
LEFT JOIN order_payments p ON p.tenant_id=t.tenant_id AND p.card_count_ledger_id=COALESCE(orig.id,t.ledger_id)
WHERE t.rn=1
GROUP BY t.card_type_id;

-- name: ReportCardRefunds :many
WITH first_types AS (
  SELECT DISTINCT ON (u.card_id) u.card_id,u.type_before
  FROM card_upgrades u
  ORDER BY u.card_id,u.at,u.id
),
typed AS (
  SELECT r.amount,
         COALESCE(u.type_after,ft.type_before,c.card_type_id) AS card_type_id,
         row_number() OVER (PARTITION BY r.id ORDER BY u.at DESC,u.id DESC) AS rn
  FROM card_refunds r
  JOIN cards c ON c.tenant_id=r.tenant_id AND c.id=r.card_id
  LEFT JOIN card_upgrades u ON u.tenant_id=r.tenant_id AND u.card_id=r.card_id AND u.at <= r.at
  LEFT JOIN first_types ft ON ft.card_id=r.card_id
  WHERE r.store_id=sqlc.arg(store_id) AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
)
SELECT card_type_id,count(*)::bigint AS refund_count,COALESCE(sum(amount),0)::text AS refund_amount
FROM typed WHERE rn=1
GROUP BY card_type_id;

-- name: ReportCardBalances :many
WITH first_types AS (
  SELECT DISTINCT ON (u.card_id) u.card_id,u.type_before
  FROM card_upgrades u
  ORDER BY u.card_id,u.at,u.id
),
latest AS (
  SELECT DISTINCT ON (l.card_id) l.card_id,l.principal_after,l.bonus_after
  FROM card_ledger l
  WHERE l.store_id=sqlc.arg(store_id) AND l.business_date <= sqlc.arg(date_to)::date
  ORDER BY l.card_id,l.at DESC,l.id DESC
),
typed AS (
  SELECT latest.card_id,latest.principal_after+latest.bonus_after AS balance,
         COALESCE(u.type_after,ft.type_before,c.card_type_id) AS card_type_id,
         row_number() OVER (PARTITION BY latest.card_id ORDER BY u.business_date DESC,u.at DESC,u.id DESC) AS rn
  FROM cards c
  JOIN latest ON latest.card_id=c.id
  LEFT JOIN card_upgrades u ON u.tenant_id=c.tenant_id AND u.card_id=c.id AND u.business_date <= sqlc.arg(date_to)::date
  LEFT JOIN first_types ft ON ft.card_id=c.id
  WHERE c.store_id=sqlc.arg(store_id)
)
SELECT card_type_id,COALESCE(sum(balance),0)::text AS ending_balance
FROM typed WHERE rn=1
GROUP BY card_type_id;

-- name: ReportCardRemainingCounts :many
WITH first_types AS (
  SELECT DISTINCT ON (u.card_id) u.card_id,u.type_before
  FROM card_upgrades u
  ORDER BY u.card_id,u.at,u.id
),
latest AS (
  SELECT DISTINCT ON (l.card_id,l.item_id) l.card_id,l.item_id,l.paid_after,l.gift_after
  FROM card_count_ledger l
  WHERE l.store_id=sqlc.arg(store_id) AND l.business_date <= sqlc.arg(date_to)::date
  ORDER BY l.card_id,l.item_id,l.at DESC,l.id DESC
),
typed AS (
  SELECT latest.card_id,latest.item_id,latest.paid_after+latest.gift_after AS counts,
         COALESCE(u.type_after,ft.type_before,c.card_type_id) AS card_type_id,
         row_number() OVER (PARTITION BY latest.card_id,latest.item_id ORDER BY u.business_date DESC,u.at DESC,u.id DESC) AS rn
  FROM cards c
  JOIN latest ON latest.card_id=c.id
  LEFT JOIN card_upgrades u ON u.tenant_id=c.tenant_id AND u.card_id=c.id AND u.business_date <= sqlc.arg(date_to)::date
  LEFT JOIN first_types ft ON ft.card_id=c.id
  WHERE c.store_id=sqlc.arg(store_id)
)
SELECT card_type_id,COALESCE(sum(counts),0)::bigint AS ending_counts
FROM typed WHERE rn=1
GROUP BY card_type_id;

-- name: CustomerSourceReport :many
WITH RECURSIVE family(root_id, customer_id, tenant_id) AS (
  SELECT cu.id,cu.id,cu.tenant_id
  FROM customers cu WHERE cu.store_id=sqlc.arg(store_id)
  UNION ALL
  SELECT f.root_id,child.id,f.tenant_id
  FROM family f
  JOIN customers child ON child.tenant_id=f.tenant_id AND child.merged_into=f.customer_id AND child.store_id=sqlc.arg(store_id)
),
imported AS (
  SELECT DISTINCT f.root_id
  FROM family f
  JOIN import_batch_records r ON r.tenant_id=f.tenant_id AND r.store_id=sqlc.arg(store_id) AND r.kind='customer' AND r.entity_id=f.customer_id
),
visits AS (
  SELECT DISTINCT f.root_id AS customer_id,c.business_date
  FROM family f
  JOIN orders o ON o.tenant_id=f.tenant_id AND o.customer_id=f.customer_id
  JOIN checkouts c ON c.tenant_id=o.tenant_id AND c.id=o.checkout_id
  WHERE o.store_id=sqlc.arg(store_id) AND o.status IN ('paid','voided')
),
first_visit AS (
  SELECT customer_id,min(business_date) AS first_date FROM visits GROUP BY customer_id
),
new_customers AS (
  SELECT fv.customer_id,cu.source_id,fv.first_date
  FROM first_visit fv JOIN customers cu ON cu.id=fv.customer_id
  WHERE cu.store_id=sqlc.arg(store_id) AND cu.phone IS NOT NULL AND cu.merged_into IS NULL
    AND fv.first_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
    AND NOT EXISTS (SELECT 1 FROM imported i WHERE i.root_id=cu.id)
),
money AS (
  SELECT f.root_id AS customer_id,c.business_date,sum(p.amount) AS amount
  FROM family f
  JOIN orders o ON o.tenant_id=f.tenant_id AND o.customer_id=f.customer_id
  JOIN checkouts c ON c.tenant_id=o.tenant_id AND c.id=o.checkout_id
  JOIN order_payments p ON p.tenant_id=o.tenant_id AND p.order_id=o.id
  WHERE o.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  GROUP BY f.root_id,c.business_date
  UNION ALL
  SELECT f.root_id,v.business_date,-sum(p.amount) AS amount
  FROM order_voids v
  JOIN orders o ON o.tenant_id=v.tenant_id AND o.id=v.order_id
  JOIN order_payments p ON p.tenant_id=o.tenant_id AND p.order_id=o.id
  JOIN family f ON f.tenant_id=o.tenant_id AND f.customer_id=o.customer_id
  WHERE v.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  GROUP BY f.root_id,v.business_date
  UNION ALL
  SELECT f.root_id,r.business_date,-sum(r.amount) AS amount
  FROM aftersale_refunds r
  JOIN aftersales a ON a.tenant_id=r.tenant_id AND a.id=r.aftersale_id AND a.status='approved'
  JOIN orders o ON o.tenant_id=a.tenant_id AND o.id=a.order_id
  JOIN family f ON f.tenant_id=o.tenant_id AND f.customer_id=o.customer_id
  WHERE r.store_id=sqlc.arg(store_id) AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  GROUP BY f.root_id,r.business_date
),
revenue AS (
  SELECT customer_id,sum(amount) AS amount FROM money GROUP BY customer_id
),
returning_customers AS (
  SELECT nc.source_id,count(*)::bigint AS n
  FROM new_customers nc
  WHERE EXISTS (SELECT 1 FROM visits v WHERE v.customer_id=nc.customer_id AND v.business_date > nc.first_date AND v.business_date <= sqlc.arg(date_to)::date)
  GROUP BY nc.source_id
),
stats AS (
  SELECT cs.id AS source_id,cs.name AS source_name,
         count(nc.customer_id)::bigint AS new_customers,
         COALESCE(sum(rv.amount),0)::text AS revenue,
         COALESCE(max(ret.n),0)::bigint AS returning
  FROM new_customers nc
  LEFT JOIN customer_sources cs ON cs.id=nc.source_id AND cs.store_id=sqlc.arg(store_id)
  LEFT JOIN revenue rv ON rv.customer_id=nc.customer_id
  LEFT JOIN returning_customers ret ON ret.source_id IS NOT DISTINCT FROM nc.source_id
  GROUP BY cs.id,cs.name
),
sources AS (
  SELECT cs.id AS source_id,cs.name AS source_name
  FROM customer_sources cs
  WHERE cs.store_id=sqlc.arg(store_id)
    AND (cs.active OR EXISTS (SELECT 1 FROM stats st WHERE st.source_id=cs.id AND st.new_customers>0))
),
all_sources AS (
  SELECT source_id,source_name,false AS null_row FROM sources
  UNION ALL
  SELECT '00000000-0000-0000-0000-000000000000'::uuid,''::text,true
)
SELECT a.source_id,a.source_name,
       COALESCE(st.new_customers,0)::bigint AS new_customers,
       COALESCE(st.revenue,'0')::text AS revenue,
       COALESCE(st.returning,0)::bigint AS returning
FROM all_sources a
LEFT JOIN stats st ON (a.null_row AND st.source_id IS NULL) OR (NOT a.null_row AND st.source_id=a.source_id)
ORDER BY a.null_row,a.source_name NULLS LAST,a.source_id;

-- name: UnidentifiedCheckoutOrders :one
SELECT COALESCE(sum(sign),0)::bigint AS count
FROM (
  SELECT 1 AS sign
  FROM orders o
  JOIN checkouts c ON c.tenant_id=o.tenant_id AND c.id=o.checkout_id
  JOIN customers cu ON cu.tenant_id=o.tenant_id AND cu.id=o.customer_id
  WHERE o.store_id=sqlc.arg(store_id) AND c.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND cu.phone IS NULL
  UNION ALL
  SELECT -1
  FROM order_voids v
  JOIN orders o ON o.tenant_id=v.tenant_id AND o.id=v.order_id
  JOIN customers cu ON cu.tenant_id=o.tenant_id AND cu.id=o.customer_id
  WHERE v.store_id=sqlc.arg(store_id) AND v.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND cu.phone IS NULL
) x;
