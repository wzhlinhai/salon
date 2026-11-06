-- +goose Up
-- 导入的历史卡只追加 import 流水，不算开卡、充值或调整，不产生销售业绩。
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void','refund','import'));
ALTER TABLE card_count_ledger DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume','void','refund','import'));
ALTER TABLE card_points_ledger DROP CONSTRAINT card_points_ledger_kind_check,
 ADD CONSTRAINT card_points_ledger_kind_check CHECK (kind IN ('open','recharge','upgrade','adjust','import'));
-- 导入的次卡可能已经用完，剩余购买次数为零；开卡仍要求至少一次。
ALTER TABLE card_items DROP CONSTRAINT card_items_paid_count_opened_check,
 ADD CONSTRAINT card_items_paid_count_opened_check CHECK (paid_count_opened BETWEEN 0 AND 9999);

CREATE TABLE import_batches (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             uuid NOT NULL,
    store_id              uuid NOT NULL,
    status                text NOT NULL DEFAULT 'previewed' CHECK (status IN ('previewed','committed','undone')),
    file_name             text NOT NULL CHECK (btrim(file_name) <> ''),
    declared_customers    int NOT NULL DEFAULT 0 CHECK (declared_customers >= 0),
    declared_stored_cards int NOT NULL DEFAULT 0 CHECK (declared_stored_cards >= 0),
    declared_principal    numeric(14,2) NOT NULL DEFAULT 0 CHECK (declared_principal >= 0),
    declared_bonus        numeric(14,2) NOT NULL DEFAULT 0 CHECK (declared_bonus >= 0),
    declared_count_cards  int NOT NULL DEFAULT 0 CHECK (declared_count_cards >= 0),
    file_customers        int NOT NULL DEFAULT 0 CHECK (file_customers >= 0),
    file_stored_cards     int NOT NULL DEFAULT 0 CHECK (file_stored_cards >= 0),
    file_principal        numeric(14,2) NOT NULL DEFAULT 0 CHECK (file_principal >= 0),
    file_bonus            numeric(14,2) NOT NULL DEFAULT 0 CHECK (file_bonus >= 0),
    file_count_cards      int NOT NULL DEFAULT 0 CHECK (file_count_cards >= 0),
    written_customers     int NOT NULL DEFAULT 0 CHECK (written_customers >= 0),
    written_stored_cards  int NOT NULL DEFAULT 0 CHECK (written_stored_cards >= 0),
    written_principal     numeric(14,2) NOT NULL DEFAULT 0 CHECK (written_principal >= 0),
    written_bonus         numeric(14,2) NOT NULL DEFAULT 0 CHECK (written_bonus >= 0),
    written_count_cards   int NOT NULL DEFAULT 0 CHECK (written_count_cards >= 0),
    created_by            uuid NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT clock_timestamp(),
    committed_by          uuid,
    committed_at          timestamptz,
    undone_by             uuid,
    undone_at             timestamptz,
    CHECK ((status = 'committed') = (committed_at IS NOT NULL AND committed_by IS NOT NULL) OR status = 'undone'),
    CHECK (status <> 'undone' OR committed_at IS NOT NULL),
    CHECK ((undone_at IS NULL) = (undone_by IS NULL)),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id),
    FOREIGN KEY (tenant_id, committed_by) REFERENCES staff (tenant_id, id),
    FOREIGN KEY (tenant_id, undone_by) REFERENCES staff (tenant_id, id)
);

CREATE TABLE import_batch_rows (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL,
    store_id        uuid NOT NULL,
    batch_id        uuid NOT NULL,
    sheet           text NOT NULL CHECK (sheet IN ('顾客','储值卡','次卡套餐')),
    row_number      int NOT NULL CHECK (row_number >= 2),
    kind            text NOT NULL CHECK (kind IN ('customer','stored_value','count_package')),
    source_key      text NOT NULL CHECK (btrim(source_key) <> ''),
    customer_source_key text,
    name            text,
    phone           text CHECK (phone ~ '^1[0-9]{10}$'),
    gender          text CHECK (gender IN ('male','female')),
    card_type_name  text,
    card_type_id    uuid,
    item_name       text,
    item_id         uuid,
    principal       numeric(10,2) NOT NULL DEFAULT 0 CHECK (principal >= 0),
    bonus           numeric(10,2) NOT NULL DEFAULT 0 CHECK (bonus >= 0),
    points          int CHECK (points BETWEEN 0 AND 99999999),
    paid_count      int CHECK (paid_count BETWEEN 0 AND 9999),
    gift_count      int CHECK (gift_count BETWEEN 0 AND 9999),
    expires_on      date,
    error            text,
    action          text CHECK (action IN ('created','linked')),
    entity_id       uuid,
    CHECK (kind = 'customer' OR customer_source_key IS NOT NULL),
    UNIQUE (batch_id, sheet, row_number),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, batch_id) REFERENCES import_batches (tenant_id, id),
    FOREIGN KEY (tenant_id, card_type_id) REFERENCES card_types (tenant_id, id),
    FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id)
);
CREATE INDEX import_batch_rows_batch ON import_batch_rows (batch_id, sheet, row_number);

CREATE TABLE import_batch_records (
    tenant_id  uuid NOT NULL,
    store_id   uuid NOT NULL,
    batch_id   uuid NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('customer','card','card_item','card_ledger','card_count_ledger','card_points_ledger')),
    entity_id  uuid NOT NULL,
    source_key text NOT NULL DEFAULT '',
    created    boolean NOT NULL,
    PRIMARY KEY (tenant_id, batch_id, kind, entity_id, source_key),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, batch_id) REFERENCES import_batches (tenant_id, id)
);
CREATE INDEX import_batch_records_batch ON import_batch_records (tenant_id, batch_id);
CREATE INDEX import_batch_records_cards ON import_batch_records (tenant_id, store_id, kind, source_key) WHERE kind = 'card';

ALTER TABLE import_batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON import_batches
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE import_batch_rows ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON import_batch_rows
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE import_batch_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON import_batch_records
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON import_batches TO salon_app;
GRANT SELECT, INSERT, UPDATE ON import_batch_rows TO salon_app;
GRANT SELECT, INSERT ON import_batch_records TO salon_app;

ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export'));

-- 撤销只删除本批次记录为 created 的行。任何外部引用都先列出来；有引用时不删除。
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
DROP FUNCTION IF EXISTS undo_import_batch(uuid, uuid);
DELETE FROM card_points_ledger WHERE kind = 'import';
DELETE FROM card_count_ledger WHERE kind = 'import';
DELETE FROM card_ledger WHERE kind = 'import';
-- 导入的次卡可能剩余购买次数为 0；旧约束要求 1–9999。把 0 改成 1，
-- 比删掉整个 card_item（丢失项目、赠送次数和单价）数据损失更小，且导入流水已在上一步删除。
UPDATE card_items SET paid_count_opened = 1 WHERE paid_count_opened = 0;
DELETE FROM audit_log WHERE target_kind IN ('import_batch','data_export');
DROP TABLE import_batch_records;
DROP TABLE import_batch_rows;
DROP TABLE import_batches;
ALTER TABLE card_items DROP CONSTRAINT card_items_paid_count_opened_check,
 ADD CONSTRAINT card_items_paid_count_opened_check CHECK (paid_count_opened BETWEEN 1 AND 9999);
ALTER TABLE card_points_ledger DROP CONSTRAINT card_points_ledger_kind_check,
 ADD CONSTRAINT card_points_ledger_kind_check CHECK (kind IN ('open','recharge','upgrade','adjust'));
ALTER TABLE card_count_ledger DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume','void','refund'));
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void','refund'));
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption'));
