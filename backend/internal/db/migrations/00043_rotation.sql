-- +goose Up
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','rotation_queue'));

CREATE TABLE rotation_queues (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 name text NOT NULL CHECK (name <> '' AND char_length(name) <= 20),
 active boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (store_id, name),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id)
);
CREATE TABLE rotation_members (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 queue_id uuid NOT NULL,
 staff_id uuid NOT NULL,
 sort_order integer NOT NULL CHECK (sort_order >= 1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (queue_id, staff_id),
 UNIQUE (queue_id, sort_order),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, queue_id) REFERENCES rotation_queues (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
CREATE TABLE rotation_turns (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 queue_id uuid NOT NULL,
 business_date date NOT NULL,
 staff_id uuid NOT NULL,
 position integer NOT NULL CHECK (position >= 1),
 status text NOT NULL CHECK (status IN ('off','waiting','serving','resting','covered')),
 half_pending boolean NOT NULL DEFAULT false,
 rest_until timestamptz,
 serving_order_id uuid,
 serving_designated boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((status = 'resting') = (rest_until IS NOT NULL)),
 CHECK (status = 'serving' OR (serving_order_id IS NULL AND NOT serving_designated)),
 UNIQUE (tenant_id, id),
 UNIQUE (queue_id, business_date, staff_id),
 UNIQUE (queue_id, business_date, position),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, queue_id) REFERENCES rotation_queues (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, serving_order_id) REFERENCES orders (tenant_id, id)
);
CREATE TABLE rotation_events (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 queue_id uuid NOT NULL,
 business_date date NOT NULL,
 staff_id uuid,
 kind text NOT NULL CHECK (kind IN ('join','leave','assign','release','skip','auto_skip','cover','uncover','rest','rest_end','drag','advance_whole','advance_half','advance_designated_no_turn')),
 reason text,
 note text,
 from_position integer CHECK (from_position IS NULL OR from_position >= 1),
 to_position integer CHECK (to_position IS NULL OR to_position >= 1),
 minutes integer CHECK (minutes IS NULL OR (minutes >= 1 AND minutes <= 240)),
 order_id uuid,
 designated boolean,
 actor_staff_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (reason IS NULL OR (reason <> '' AND char_length(reason) <= 200)),
 CHECK (note IS NULL OR char_length(note) <= 200),
 CHECK (kind NOT IN ('skip','auto_skip','drag') OR reason IS NOT NULL),
 CHECK (kind <> 'rest' OR minutes IS NOT NULL),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, queue_id) REFERENCES rotation_queues (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, actor_staff_id) REFERENCES staff (tenant_id, id)
);
CREATE TABLE rotation_half_items (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 item_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (store_id, item_id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id)
);
CREATE INDEX rotation_members_queue ON rotation_members (store_id, queue_id, sort_order);
CREATE INDEX rotation_turns_day ON rotation_turns (store_id, queue_id, business_date, position);
CREATE INDEX rotation_events_range ON rotation_events (store_id, business_date, created_at, id);
CREATE INDEX rotation_events_queue ON rotation_events (store_id, queue_id, business_date, created_at, id);
ALTER TABLE rotation_queues ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rotation_queues
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE rotation_members ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rotation_members
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE rotation_turns ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rotation_turns
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE rotation_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rotation_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE rotation_half_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rotation_half_items
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON rotation_queues TO salon_app;
GRANT SELECT, INSERT, DELETE ON rotation_members TO salon_app;
GRANT UPDATE (sort_order) ON rotation_members TO salon_app;
GRANT SELECT, INSERT, UPDATE ON rotation_turns TO salon_app;
GRANT SELECT, INSERT ON rotation_events TO salon_app;
GRANT SELECT, INSERT, DELETE ON rotation_half_items TO salon_app;

-- +goose Down
DROP TABLE rotation_half_items;
DROP TABLE rotation_events;
DROP TABLE rotation_turns;
DROP TABLE rotation_members;
DROP TABLE rotation_queues;
DELETE FROM audit_log WHERE target_kind = 'rotation_queue';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption'));
