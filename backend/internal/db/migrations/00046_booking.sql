-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

ALTER TABLE items
 ADD COLUMN duration_minutes integer NOT NULL DEFAULT 0 CHECK (duration_minutes BETWEEN 0 AND 600),
 ADD COLUMN bookable boolean NOT NULL DEFAULT true;

ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card','order','red_packet','debt','appointment')) = (customer_id IS NOT NULL));

CREATE TABLE appointments (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 customer_id uuid,
 kind text NOT NULL CHECK (kind IN ('appointment','block')),
 starts_at timestamptz NOT NULL,
 ends_at timestamptz NOT NULL,
 status text NOT NULL CHECK (status IN ('pending','confirmed','completed','cancelled','no_show')),
 note text CHECK (note IS NULL OR char_length(note) <= 200),
 order_id uuid,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (ends_at > starts_at),
 CHECK ((kind = 'appointment') = (customer_id IS NOT NULL)),
 CHECK (kind <> 'block' OR status IN ('confirmed','cancelled')),
 CHECK (kind <> 'block' OR order_id IS NULL),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id),
 EXCLUDE USING gist (tenant_id WITH =, staff_id WITH =, tstzrange(starts_at, ends_at) WITH &&) WHERE (status IN ('pending','confirmed'))
);
CREATE TABLE appointment_items (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 appointment_id uuid NOT NULL,
 item_id uuid NOT NULL,
 position integer NOT NULL CHECK (position >= 1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (appointment_id, item_id),
 UNIQUE (appointment_id, position),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, appointment_id) REFERENCES appointments (tenant_id, id),
 FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id)
);
CREATE TABLE appointment_events (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 appointment_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('create','reschedule','confirm','complete','cancel','no_show')),
 from_starts_at timestamptz,
 from_ends_at timestamptz,
 to_starts_at timestamptz NOT NULL,
 to_ends_at timestamptz NOT NULL,
 from_staff_id uuid,
 to_staff_id uuid NOT NULL,
 reason text,
 note text,
 actor_staff_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((from_starts_at IS NULL) = (from_ends_at IS NULL)),
 CHECK ((from_staff_id IS NULL) = (from_starts_at IS NULL)),
 CHECK (to_ends_at > to_starts_at),
 CHECK (reason IS NULL OR (reason <> '' AND char_length(reason) <= 200)),
 CHECK (note IS NULL OR char_length(note) <= 200),
 CHECK (reason IS NULL OR reason IN ('顾客取消','门店取消','改约','其他')),
 CHECK (reason IS DISTINCT FROM '其他' OR note IS NOT NULL),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, appointment_id) REFERENCES appointments (tenant_id, id),
 FOREIGN KEY (tenant_id, from_staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, to_staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, actor_staff_id) REFERENCES staff (tenant_id, id)
);
CREATE INDEX appointments_range ON appointments (store_id, starts_at, id);
CREATE INDEX appointments_occupied ON appointments (store_id, staff_id, starts_at, ends_at) WHERE status IN ('pending','confirmed');
CREATE INDEX appointment_items_lookup ON appointment_items (store_id, appointment_id, position);
CREATE INDEX appointment_events_lookup ON appointment_events (store_id, appointment_id, created_at, id);
ALTER TABLE appointments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON appointments
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE appointment_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON appointment_items
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE appointment_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON appointment_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON appointments TO salon_app;
GRANT UPDATE (status, starts_at, ends_at, staff_id, customer_id, order_id) ON appointments TO salon_app;
GRANT SELECT, INSERT ON appointment_items TO salon_app;
GRANT SELECT, INSERT ON appointment_events TO salon_app;

-- 撤销导入时，预约也属于顾客的外部引用；重新定义函数只增加这一项检查。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION undo_import_batch(p_batch_id uuid, p_store_id uuid) RETURNS text[]
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    b import_batches%ROWTYPE;
    refs text[] := ARRAY[]::text[];
    created_customers uuid[] := ARRAY[]::uuid[];
    created_cards uuid[] := ARRAY[]::uuid[];
    batch_ledgers uuid[] := ARRAY[]::uuid[];
    batch_counts uuid[] := ARRAY[]::uuid[];
    batch_points uuid[] := ARRAY[]::uuid[];
    r record;
BEGIN
    SELECT * INTO b FROM import_batches WHERE id = p_batch_id;
    IF NOT FOUND
        OR b.tenant_id IS DISTINCT FROM NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR b.store_id IS DISTINCT FROM p_store_id THEN
        RAISE EXCEPTION '导入批次不存在';
    END IF;
    IF b.status <> 'committed' THEN
        RETURN ARRAY['导入批次不是已提交状态'];
    END IF;

    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO created_customers
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'customer' AND created;
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO created_cards
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card' AND created;
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO batch_ledgers
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card_ledger';
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO batch_counts
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card_count_ledger';
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO batch_points
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card_points_ledger';

    -- 先锁顾客再锁卡，按 UUID 顺序；与开卡/充值/合并一样先锁父行再写子表。
    -- 外部引用正在插入时会先拿到父行的 KEY SHARE，这里等它提交后再检查，返回 409 而不是外键错误。
    PERFORM 1 FROM customers WHERE tenant_id = b.tenant_id AND id = ANY(created_customers) ORDER BY id FOR UPDATE;
    PERFORM 1 FROM cards WHERE tenant_id = b.tenant_id AND id = ANY(created_cards) ORDER BY id FOR UPDATE;

    FOR r IN SELECT c.id, c.name, c.merged_into FROM customers c WHERE c.id = ANY(created_customers) LOOP
        IF r.merged_into IS NOT NULL OR EXISTS (SELECT 1 FROM customers x WHERE x.merged_into = r.id) THEN
            refs := refs || format('顾客 %s 已合并', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM orders o WHERE o.tenant_id = b.tenant_id AND o.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有单据', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM appointments a WHERE a.tenant_id = b.tenant_id AND a.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有预约', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM debts d WHERE d.tenant_id = b.tenant_id AND d.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有欠款', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM red_packets p WHERE p.tenant_id = b.tenant_id AND p.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有红包', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM customer_safety_labels l WHERE l.tenant_id = b.tenant_id AND l.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有安全标签', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM cards c WHERE c.tenant_id = b.tenant_id AND c.customer_id = r.id AND NOT c.id = ANY(created_cards)) THEN
            refs := refs || format('顾客 %s 还有其他卡', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM audit_log a WHERE a.tenant_id = b.tenant_id AND a.target_kind = 'customer' AND a.target_id = r.id AND a.at > b.committed_at) THEN
            refs := refs || format('顾客 %s 已被修改', COALESCE(r.name, r.id::text));
        END IF;
    END LOOP;

    FOR r IN SELECT c.id, c.status FROM cards c WHERE c.id = ANY(created_cards) LOOP
        IF r.status <> 'active' THEN
            refs := refs || format('卡 %s 已退', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_ledger l WHERE l.tenant_id = b.tenant_id AND l.card_id = r.id AND NOT l.id = ANY(batch_ledgers)) THEN
            refs := refs || format('卡 %s 有其他余额流水', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_count_ledger l WHERE l.tenant_id = b.tenant_id AND l.card_id = r.id AND NOT l.id = ANY(batch_counts)) THEN
            refs := refs || format('卡 %s 有其他次数流水', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_points_ledger l WHERE l.tenant_id = b.tenant_id AND l.card_id = r.id AND NOT l.id = ANY(batch_points)) THEN
            refs := refs || format('卡 %s 有其他积分流水', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_adjustments a WHERE a.tenant_id = b.tenant_id AND a.card_id = r.id) THEN
            refs := refs || format('卡 %s 有调整单', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_upgrades u WHERE u.tenant_id = b.tenant_id AND u.card_id = r.id) THEN
            refs := refs || format('卡 %s 有升级记录', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_refunds f WHERE f.tenant_id = b.tenant_id AND f.card_id = r.id) THEN
            refs := refs || format('卡 %s 有退款记录', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_transfers t WHERE t.tenant_id = b.tenant_id AND (t.from_card_id = r.id OR t.to_card_id = r.id)) THEN
            refs := refs || format('卡 %s 有转余额记录', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM audit_log a WHERE a.tenant_id = b.tenant_id AND a.target_kind = 'card' AND a.target_id = r.id AND a.at > b.committed_at) THEN
            refs := refs || format('卡 %s 已被修改', r.id::text);
        END IF;
    END LOOP;

    IF EXISTS (SELECT 1 FROM performance_entries e WHERE e.card_ledger_id = ANY(batch_ledgers)) THEN
        refs := refs || '导入的卡流水已产生业绩';
    END IF;
    IF EXISTS (SELECT 1 FROM order_payments p WHERE p.card_ledger_id = ANY(batch_ledgers) OR p.card_count_ledger_id = ANY(batch_counts)) THEN
        refs := refs || '导入的卡流水已用于支付';
    END IF;
    IF EXISTS (SELECT 1 FROM card_ledger_sales s WHERE s.ledger_id = ANY(batch_ledgers)) THEN
        refs := refs || '导入的卡流水有业绩归属人';
    END IF;
    IF EXISTS (
        SELECT 1 FROM card_items i
         WHERE i.card_id = ANY(created_cards)
           AND NOT EXISTS (
               SELECT 1 FROM import_batch_records rec
                WHERE rec.tenant_id = b.tenant_id AND rec.batch_id = p_batch_id
                  AND rec.kind = 'card_item' AND rec.entity_id = i.card_id AND rec.source_key = i.item_id::text
           )
    ) THEN
        refs := refs || '导入的卡有新增项目';
    END IF;

    IF cardinality(refs) > 0 THEN
        RETURN refs;
    END IF;

    DELETE FROM card_points_ledger WHERE id = ANY(batch_points);
    DELETE FROM card_count_ledger WHERE id = ANY(batch_counts);
    DELETE FROM card_ledger WHERE id = ANY(batch_ledgers);
    DELETE FROM card_items WHERE card_id = ANY(created_cards);
    DELETE FROM cards WHERE id = ANY(created_cards);
    DELETE FROM customers WHERE id = ANY(created_customers);
    DELETE FROM import_batch_records WHERE batch_id = p_batch_id;
    RETURN ARRAY[]::text[];
END $$;
REVOKE EXECUTE ON FUNCTION undo_import_batch(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION undo_import_batch(uuid, uuid) TO salon_app;
-- +goose StatementEnd

-- +goose Down
DROP TABLE appointment_events;
DROP TABLE appointment_items;
DROP TABLE appointments;
DELETE FROM audit_log WHERE target_kind IN ('appointment','appointment_block');
ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card','order','red_packet','debt')) = (customer_id IS NOT NULL));
ALTER TABLE items DROP COLUMN bookable, DROP COLUMN duration_minutes;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION undo_import_batch(p_batch_id uuid, p_store_id uuid) RETURNS text[]
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    b import_batches%ROWTYPE;
    refs text[] := ARRAY[]::text[];
    created_customers uuid[] := ARRAY[]::uuid[];
    created_cards uuid[] := ARRAY[]::uuid[];
    batch_ledgers uuid[] := ARRAY[]::uuid[];
    batch_counts uuid[] := ARRAY[]::uuid[];
    batch_points uuid[] := ARRAY[]::uuid[];
    r record;
BEGIN
    SELECT * INTO b FROM import_batches WHERE id = p_batch_id;
    IF NOT FOUND
        OR b.tenant_id IS DISTINCT FROM NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR b.store_id IS DISTINCT FROM p_store_id  THEN
        RAISE EXCEPTION '导入批次不存在';
    END IF;
    IF b.status <> 'committed' THEN
        RETURN ARRAY['导入批次不是已提交状态'];
    END IF;

    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO created_customers
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'customer' AND created;
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO created_cards
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card' AND created;
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO batch_ledgers
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card_ledger';
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO batch_counts
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card_count_ledger';
    SELECT COALESCE(array_agg(entity_id), ARRAY[]::uuid[]) INTO batch_points
      FROM import_batch_records
     WHERE tenant_id = b.tenant_id AND batch_id = p_batch_id AND kind = 'card_points_ledger';

    -- 先锁顾客再锁卡，按 UUID 顺序；与开卡/充值/合并一样先锁父行再写子表。
    -- 外部引用正在插入时会先拿到父行的 KEY SHARE，这里等它提交后再检查，返回 409 而不是外键错误。
    PERFORM 1 FROM customers WHERE tenant_id = b.tenant_id AND id = ANY(created_customers) ORDER BY id FOR UPDATE ;
    PERFORM 1 FROM cards WHERE tenant_id = b.tenant_id AND id = ANY(created_cards) ORDER BY id FOR UPDATE;

    FOR r IN SELECT c.id, c.name, c.merged_into FROM customers c WHERE c.id = ANY(created_customers) LOOP
        IF r.merged_into IS NOT NULL OR EXISTS (SELECT 1 FROM customers x WHERE x.merged_into = r.id) THEN
            refs := refs || format('顾客 %s 已合并', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM orders o WHERE o.tenant_id = b.tenant_id AND o.customer_id = r.id)  THEN
            refs := refs || format('顾客 %s 已有单据', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM debts d WHERE d.tenant_id = b.tenant_id AND d.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有欠款', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM red_packets p WHERE p.tenant_id = b.tenant_id AND p.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有红包', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM customer_safety_labels l WHERE l.tenant_id = b.tenant_id AND l.customer_id = r.id) THEN
            refs := refs || format('顾客 %s 已有安全标签', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM cards c WHERE c.tenant_id = b.tenant_id AND c.customer_id = r.id AND NOT c.id = ANY(created_cards)) THEN
            refs := refs || format('顾客 %s 还有其他卡', COALESCE(r.name, r.id::text));
        ELSIF EXISTS (SELECT 1 FROM audit_log a WHERE a.tenant_id = b.tenant_id AND a.target_kind = 'customer' AND a.target_id = r.id AND a.at > b.committed_at)  THEN
            refs := refs || format('顾客 %s 已被修改', COALESCE(r.name, r.id::text));
        END IF;
    END LOOP;

    FOR r IN SELECT c.id, c.status FROM cards c WHERE c.id = ANY(created_cards) LOOP
        IF r.status <> 'active' THEN
            refs := refs || format('卡 %s 已退', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_ledger l WHERE l.tenant_id = b.tenant_id AND l.card_id = r.id AND NOT l.id = ANY(batch_ledgers))  THEN
            refs := refs || format('卡 %s 有其他余额流水', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_count_ledger l WHERE l.tenant_id = b.tenant_id AND l.card_id = r.id AND NOT l.id = ANY(batch_counts)) THEN
            refs := refs || format('卡 %s 有其他次数流水', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_points_ledger l WHERE l.tenant_id = b.tenant_id AND l.card_id = r.id AND NOT l.id = ANY(batch_points)) THEN
            refs := refs || format('卡 %s 有其他积分流水', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_adjustments a WHERE a.tenant_id = b.tenant_id AND a.card_id = r.id) THEN
            refs := refs || format('卡 %s 有调整单', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_upgrades u WHERE u.tenant_id = b.tenant_id AND u.card_id = r.id) THEN
            refs := refs || format('卡 %s 有升级记录', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_refunds f WHERE f.tenant_id = b.tenant_id AND f.card_id = r.id) THEN
            refs := refs || format('卡 %s 有退款记录', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM card_transfers t WHERE t.tenant_id = b.tenant_id AND (t.from_card_id = r.id OR t.to_card_id = r.id)) THEN
            refs := refs || format('卡 %s 有转余额记录', r.id::text);
        ELSIF EXISTS (SELECT 1 FROM audit_log a WHERE a.tenant_id = b.tenant_id AND a.target_kind = 'card' AND a.target_id = r.id AND a.at > b.committed_at) THEN
            refs := refs || format('卡 %s 已被修改', r.id::text);
        END IF;
    END LOOP;

    IF EXISTS (SELECT 1 FROM performance_entries e WHERE e.card_ledger_id = ANY(batch_ledgers)) THEN
        refs := refs || '导入的卡流水已产生业绩';
    END IF;
    IF EXISTS (SELECT 1 FROM order_payments p WHERE p.card_ledger_id = ANY(batch_ledgers) OR p.card_count_ledger_id = ANY(batch_counts)) THEN
        refs := refs || '导入的卡流水已用于支付';
    END IF;
    IF EXISTS (SELECT 1 FROM card_ledger_sales s WHERE s.ledger_id = ANY(batch_ledgers)) THEN
        refs := refs || '导入的卡流水有业绩归属人';
    END IF;
    IF EXISTS (
        SELECT 1 FROM card_items i
         WHERE i.card_id = ANY(created_cards)
           AND NOT EXISTS (
               SELECT 1 FROM import_batch_records rec
                WHERE rec.tenant_id = b.tenant_id AND rec.batch_id = p_batch_id
                  AND rec.kind = 'card_item' AND rec.entity_id = i.card_id AND rec.source_key = i.item_id::text
           )
    ) THEN
        refs := refs || '导入的卡有新增项目';
    END IF;

    IF cardinality(refs) > 0 THEN
        RETURN refs;
    END IF;

    DELETE FROM card_points_ledger WHERE id = ANY(batch_points);
    DELETE FROM card_count_ledger WHERE id = ANY(batch_counts);
    DELETE FROM card_ledger WHERE id = ANY(batch_ledgers);
    DELETE FROM card_items WHERE card_id = ANY(created_cards);
    DELETE FROM cards WHERE id = ANY(created_cards);
    DELETE FROM customers WHERE id = ANY(created_customers) ;
    DELETE FROM import_batch_records WHERE batch_id = p_batch_id;
    RETURN ARRAY[]::text[];
END $$;
REVOKE EXECUTE ON FUNCTION undo_import_batch(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION undo_import_batch(uuid, uuid) TO salon_app;
-- +goose StatementEnd
