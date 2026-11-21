-- name: CustomerOverviewSegments :one
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
base AS (
  SELECT cu.* FROM customers cu
  WHERE cu.store_id=sqlc.arg(store_id)
    AND cu.merged_into IS NULL
    AND cu.phone IS NOT NULL
    AND (sqlc.narg(responsible_staff_id)::uuid IS NULL OR cu.responsible_staff_id = sqlc.narg(responsible_staff_id))
    AND (sqlc.narg(self_id)::uuid IS NULL OR cu.responsible_staff_id = sqlc.narg(self_id))
    AND (sqlc.arg(query)::text = '' OR
      CASE
        WHEN sqlc.arg(query)::text ~ '^[0-9]{11}$' THEN cu.phone = sqlc.arg(query)
        WHEN sqlc.arg(query)::text ~ '^[0-9]{4}$' THEN right(cu.phone, 4) = sqlc.arg(query)
        ELSE strpos(lower(coalesce(cu.name, '')), lower(sqlc.arg(query))) > 0
      END)
),
paid_orders AS (
  SELECT o.id,o.customer_id,o.business_date
  FROM orders o
  JOIN base b ON b.id=o.customer_id
  WHERE o.store_id=sqlc.arg(store_id) AND o.status='paid'
),
visits AS (
  SELECT customer_id,
         count(*)::int AS visit_count,
         min(business_date) AS first_visit_date,
         max(business_date) AS last_visit_date,
         count(DISTINCT business_date)::int AS distinct_dates
  FROM paid_orders GROUP BY customer_id
),
valid_cards AS (
  SELECT c.id,c.customer_id,l.principal_after,l.bonus_after
  FROM cards c
  JOIN base b ON b.id=c.customer_id
  JOIN LATERAL (SELECT principal_after,bonus_after FROM card_ledger WHERE card_id=c.id AND store_id=sqlc.arg(store_id) ORDER BY at DESC,id DESC LIMIT 1) l ON true
  WHERE c.store_id=sqlc.arg(store_id) AND c.status='active'
    AND (c.expires_on IS NULL OR c.expires_on >= sqlc.arg(today)::date)
),
balances AS (
  SELECT customer_id,sum(principal_after+bonus_after) AS stored_balance
  FROM valid_cards GROUP BY customer_id
),
card_counts AS (
  SELECT vc.customer_id,sum(l.paid_after+l.gift_after)::bigint AS count_remaining
  FROM valid_cards vc
  JOIN card_items ci ON ci.card_id=vc.id AND ci.store_id=sqlc.arg(store_id)
  JOIN LATERAL (SELECT paid_after,gift_after FROM card_count_ledger WHERE card_id=ci.card_id AND item_id=ci.item_id AND store_id=sqlc.arg(store_id) ORDER BY at DESC,id DESC LIMIT 1) l ON true
  GROUP BY vc.customer_id
),
next_appointments AS (
  SELECT DISTINCT ON (a.customer_id) a.customer_id,a.id
  FROM appointments a
  JOIN base b ON b.id=a.customer_id
  WHERE a.store_id=sqlc.arg(store_id) AND a.kind='appointment'
    AND a.status IN ('pending','confirmed') AND a.starts_at >= sqlc.arg(now)::timestamptz
  ORDER BY a.customer_id,a.starts_at,a.id
),
metrics AS (
  SELECT b.*,
         COALESCE(v.visit_count,0)::int AS visit_count,
         v.first_visit_date,
         v.last_visit_date,
         CASE WHEN v.last_visit_date IS NULL THEN NULL ELSE (sqlc.arg(today)::date-v.last_visit_date)::int END AS days_since_last_visit,
         CASE WHEN v.distinct_dates >= 2 THEN round((v.last_visit_date-v.first_visit_date)::numeric/(v.distinct_dates-1))::int END AS cycle_days
  FROM base b LEFT JOIN visits v ON v.customer_id=b.id
),
enriched AS (
  SELECT m.*,
         CASE WHEN m.cycle_days IS NOT NULL AND m.days_since_last_visit-m.cycle_days > 0
              THEN m.days_since_last_visit-m.cycle_days END AS overdue_days
  FROM metrics m
),
metric_rows AS (
  SELECT e.*,
         COALESCE(ba.stored_balance,0) AS stored_balance,
         COALESCE(cc.count_remaining,0)::bigint AS count_remaining,
         na.id AS next_appointment_id,
         EXISTS (
           SELECT 1 FROM paid_orders po
           WHERE po.customer_id=e.id AND date_trunc('month',po.business_date)::date=date_trunc('month',sqlc.arg(today)::date)::date
         ) AS visited_this_month,
         EXISTS (
           SELECT 1 FROM appointments a
           WHERE a.store_id=sqlc.arg(store_id) AND a.kind='appointment'
             AND a.status IN ('pending','confirmed','completed')
             AND a.customer_id=e.id
             AND a.starts_at >= sqlc.arg(today_start)::timestamptz AND a.starts_at < sqlc.arg(today_end)::timestamptz
         ) AS has_today_appointment,
         EXISTS (
           SELECT 1 FROM orders o
           WHERE o.store_id=sqlc.arg(store_id) AND o.customer_id=e.id
             AND o.status NOT IN ('voided','merged') AND o.business_date=sqlc.arg(today)::date
         ) AS arrived,
         (e.overdue_days > 0 AND e.days_since_last_visit <= sqlc.arg(stale_days)::int AND na.id IS NULL) AS due,
         (COALESCE(ba.stored_balance,0) > 0 OR COALESCE(cc.count_remaining,0) > 0) AS has_balance,
         (e.first_visit_date BETWEEN (sqlc.arg(today)::date-(sqlc.arg(new_days)::int-1)) AND sqlc.arg(today)::date
          AND NOT EXISTS (SELECT 1 FROM imported i WHERE i.root_id=e.id)) AS is_new
  FROM enriched e
  LEFT JOIN balances ba ON ba.customer_id=e.id
  LEFT JOIN card_counts cc ON cc.customer_id=e.id
  LEFT JOIN next_appointments na ON na.customer_id=e.id
)
SELECT count(*)::bigint AS all_count,
       count(*) FILTER (WHERE visited_this_month)::bigint AS visited_this_month,
       count(*) FILTER (WHERE has_today_appointment)::bigint AS today_count,
       count(*) FILTER (WHERE has_today_appointment AND arrived)::bigint AS today_arrived,
       count(*) FILTER (WHERE due)::bigint AS due_count,
       count(*) FILTER (WHERE due AND has_balance)::bigint AS due_with_balance,
       count(*) FILTER (WHERE has_balance)::bigint AS balance_count,
       COALESCE(sum(stored_balance) FILTER (WHERE has_balance),0)::text AS stored_total,
       count(*) FILTER (WHERE is_new)::bigint AS new_count,
       count(*) FILTER (WHERE is_new AND visit_count=1)::bigint AS new_single_visit,
       count(*) FILTER (WHERE visit_count >= 1 AND days_since_last_visit > sqlc.arg(stale_days)::int)::bigint AS sleeping_count
FROM metric_rows;
