-- +goose Up
ALTER TABLE order_items ADD CONSTRAINT order_items_tenant_store_id_key UNIQUE (tenant_id,store_id,id);

CREATE TABLE order_staff_changes (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 order_id uuid NOT NULL,
 order_item_id uuid NOT NULL,
 business_date date NOT NULL,
 before_staff jsonb NOT NULL CHECK (jsonb_typeof(before_staff) = 'array'),
 after_staff jsonb NOT NULL CHECK (jsonb_typeof(after_staff) = 'array'),
 changed_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, order_id) REFERENCES orders (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id, order_item_id) REFERENCES order_items (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, changed_by) REFERENCES staff (tenant_id, id)
);
CREATE INDEX order_staff_changes_order ON order_staff_changes (store_id, order_id, created_at, id);
ALTER TABLE order_staff_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_staff_changes
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON order_staff_changes TO salon_app;

ALTER TABLE performance_entries
 ADD COLUMN staff_change_id uuid,
 ADD FOREIGN KEY (tenant_id, store_id, staff_change_id) REFERENCES order_staff_changes (tenant_id, store_id, id),
 DROP CONSTRAINT performance_entries_reversal_source_check,
 ADD CONSTRAINT performance_entries_reversal_source_check CHECK (
  (void_id IS NULL OR aftersale_id IS NULL)
  AND (void_id IS NULL OR staff_change_id IS NULL)
  AND (aftersale_id IS NULL OR staff_change_id IS NULL)
  AND ((void_id IS NULL AND aftersale_id IS NULL) OR reverses_id IS NOT NULL)
  AND (reverses_id IS NULL OR void_id IS NOT NULL OR aftersale_id IS NOT NULL OR staff_change_id IS NOT NULL)
 );
CREATE UNIQUE INDEX performance_entries_staff_change_reverses ON performance_entries (reverses_id) WHERE staff_change_id IS NOT NULL;
CREATE INDEX performance_entries_staff_change ON performance_entries (store_id, staff_change_id) WHERE staff_change_id IS NOT NULL;

ALTER TABLE guest_visits DROP CONSTRAINT guest_visits_check;
ALTER TABLE guest_visits
 ADD COLUMN staff_change_id uuid,
 ADD FOREIGN KEY (tenant_id, store_id, staff_change_id) REFERENCES order_staff_changes (tenant_id, store_id, id),
 ADD CONSTRAINT guest_visits_reversal_source_check CHECK (
  (void_id IS NULL OR staff_change_id IS NULL)
  AND (void_id IS NULL OR reverses_id IS NOT NULL)
  AND (reverses_id IS NULL OR void_id IS NOT NULL OR staff_change_id IS NOT NULL)
 );
CREATE UNIQUE INDEX guest_visits_staff_change_reverses ON guest_visits (reverses_id) WHERE staff_change_id IS NOT NULL;
CREATE INDEX guest_visits_staff_change ON guest_visits (store_id, staff_change_id) WHERE staff_change_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM order_staff_changes) THEN
  RAISE EXCEPTION '无法回滚 00058：存在改人记录，会破坏业绩和客数的追溯';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX guest_visits_staff_change;
DROP INDEX guest_visits_staff_change_reverses;
ALTER TABLE guest_visits
 DROP CONSTRAINT guest_visits_reversal_source_check,
 DROP COLUMN staff_change_id,
 ADD CONSTRAINT guest_visits_check CHECK ((void_id IS NULL) = (reverses_id IS NULL));

DROP INDEX performance_entries_staff_change;
DROP INDEX performance_entries_staff_change_reverses;
ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_reversal_source_check,
 DROP COLUMN staff_change_id,
 ADD CONSTRAINT performance_entries_reversal_source_check CHECK (((void_id IS NULL AND aftersale_id IS NULL) = (reverses_id IS NULL)) AND (void_id IS NULL OR aftersale_id IS NULL));

DROP TABLE order_staff_changes;
ALTER TABLE order_items DROP CONSTRAINT order_items_tenant_store_id_key;
