-- name: ListCustomerOverview :many
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
spend AS (
  SELECT po.customer_id,COALESCE(sum(oi.price-oi.discount),0) AS total_spend
  FROM paid_orders po
  JOIN order_items oi ON oi.order_id=po.id AND oi.store_id=sqlc.arg(store_id) AND oi.removed_at IS NULL
  GROUP BY po.customer_id
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
filtered AS (
  SELECT e.*,
         COALESCE(sp.total_spend,0) AS total_spend,
         COALESCE(ba.stored_balance,0) AS stored_balance,
         COALESCE(cc.count_remaining,0)::bigint AS count_remaining,
         na.id AS next_appointment_id
  FROM enriched e
  LEFT JOIN spend sp ON sp.customer_id=e.id
  LEFT JOIN balances ba ON ba.customer_id=e.id
  LEFT JOIN card_counts cc ON cc.customer_id=e.id
  LEFT JOIN next_appointments na ON na.customer_id=e.id
  WHERE CASE sqlc.arg(segment)::text
    WHEN 'all' THEN true
    WHEN 'today' THEN EXISTS (
      SELECT 1 FROM appointments a
      WHERE a.store_id=sqlc.arg(store_id) AND a.kind='appointment'
        AND a.status IN ('pending','confirmed','completed')
        AND a.customer_id=e.id
        AND a.starts_at >= sqlc.arg(today_start)::timestamptz AND a.starts_at < sqlc.arg(today_end)::timestamptz)
    WHEN 'due' THEN e.overdue_days > 0 AND e.days_since_last_visit <= sqlc.arg(stale_days)::int AND na.id IS NULL
    WHEN 'balance' THEN COALESCE(ba.stored_balance,0) > 0 OR COALESCE(cc.count_remaining,0) > 0
    WHEN 'new' THEN e.first_visit_date BETWEEN (sqlc.arg(today)::date-(sqlc.arg(new_days)::int-1)) AND sqlc.arg(today)::date
      AND NOT EXISTS (SELECT 1 FROM imported i WHERE i.root_id=e.id)
    WHEN 'sleeping' THEN e.visit_count >= 1 AND e.days_since_last_visit > sqlc.arg(stale_days)::int
    ELSE false
  END
)
SELECT f.id,f.tenant_id,f.store_id,f.name,f.phone,f.gender,f.source_id,
       f.responsible_staff_id,f.note,f.merged_into,f.created_by,f.created_at,f.created_by_customer,
       f.hair_texture,f.preferences,f.dislikes,
       f.visit_count,
       COALESCE(f.first_visit_date::text,'')::text AS first_visit_date,
       COALESCE(f.last_visit_date::text,'')::text AS last_visit_date,
       COALESCE(f.days_since_last_visit,-1)::int AS days_since_last_visit,
       COALESCE(f.cycle_days::int,-1)::int AS cycle_days,
       COALESCE(f.overdue_days::int,-1)::int AS overdue_days,
       f.total_spend::text AS total_spend,
       f.stored_balance::text AS stored_balance,
       f.count_remaining,
       f.next_appointment_id,
       count(*) OVER ()::bigint AS total
FROM filtered f
ORDER BY
  CASE WHEN sqlc.arg(sort)::text='last_visit' THEN f.last_visit_date END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text='overdue' THEN f.overdue_days END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text='spend' THEN f.total_spend END DESC,
  CASE WHEN sqlc.arg(sort)::text='created' THEN f.created_at END DESC,
  f.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListCustomerOverviewVisitItems :many
WITH last_visits AS (
  SELECT o.customer_id,max(o.business_date) AS business_date
  FROM orders o
  WHERE o.store_id=sqlc.arg(store_id) AND o.customer_id=ANY(sqlc.arg(customer_ids)::uuid[]) AND o.status='paid'
  GROUP BY o.customer_id
)
SELECT lv.customer_id,oi.item_name
FROM last_visits lv
JOIN orders o ON o.store_id=sqlc.arg(store_id) AND o.customer_id=lv.customer_id AND o.business_date=lv.business_date AND o.status='paid'
JOIN order_items oi ON oi.order_id=o.id AND oi.store_id=sqlc.arg(store_id) AND oi.removed_at IS NULL
GROUP BY lv.customer_id,oi.item_name
ORDER BY lv.customer_id,min(oi.added_at),oi.item_name;

-- name: ListCustomerOverviewVisitStaff :many
WITH last_visits AS (
  SELECT o.customer_id,max(o.business_date) AS business_date
  FROM orders o
  WHERE o.store_id=sqlc.arg(store_id) AND o.customer_id=ANY(sqlc.arg(customer_ids)::uuid[]) AND o.status='paid'
  GROUP BY o.customer_id
)
SELECT lv.customer_id,s.id AS staff_id,s.name
FROM last_visits lv
JOIN orders o ON o.store_id=sqlc.arg(store_id) AND o.customer_id=lv.customer_id AND o.business_date=lv.business_date AND o.status='paid'
JOIN order_items oi ON oi.order_id=o.id AND oi.store_id=sqlc.arg(store_id) AND oi.removed_at IS NULL
JOIN order_item_positions p ON p.order_item_id=oi.id AND p.store_id=sqlc.arg(store_id)
JOIN staff s ON s.id=p.staff_id
GROUP BY lv.customer_id,s.id,s.name
ORDER BY lv.customer_id,min(p.slot),s.name,s.id;

-- name: ListCustomerOverviewAppointments :many
SELECT a.id,a.starts_at,s.id AS staff_id,s.name AS staff_name
FROM appointments a
JOIN staff s ON s.id=a.staff_id
WHERE a.store_id=sqlc.arg(store_id) AND a.id=ANY(sqlc.arg(appointment_ids)::uuid[]);
