-- +goose Up
-- 顾客自助预约：可预约员工、顾客操作者字段（ADR-0011）。

ALTER TABLE staff ADD COLUMN self_bookable boolean NOT NULL DEFAULT false;

ALTER TABLE appointments ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE appointments ADD COLUMN created_by_customer uuid;
ALTER TABLE appointments ADD CONSTRAINT appointments_actor_check
    CHECK ((created_by IS NULL) <> (created_by_customer IS NULL));
ALTER TABLE appointments ADD CONSTRAINT appointments_created_by_customer_fkey
    FOREIGN KEY (tenant_id, created_by_customer) REFERENCES customers (tenant_id, id);

ALTER TABLE appointment_events ALTER COLUMN actor_staff_id DROP NOT NULL;
ALTER TABLE appointment_events ADD COLUMN actor_customer_id uuid;
ALTER TABLE appointment_events ADD CONSTRAINT appointment_events_actor_check
    CHECK ((actor_staff_id IS NULL) <> (actor_customer_id IS NULL));
ALTER TABLE appointment_events ADD CONSTRAINT appointment_events_actor_customer_fkey
    FOREIGN KEY (tenant_id, actor_customer_id) REFERENCES customers (tenant_id, id);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM appointments WHERE created_by_customer IS NOT NULL)
    OR EXISTS (SELECT 1 FROM appointment_events WHERE actor_customer_id IS NOT NULL) THEN
  RAISE EXCEPTION '无法回滚 00053：存在顾客操作者的预约数据';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE appointment_events DROP CONSTRAINT appointment_events_actor_customer_fkey;
ALTER TABLE appointment_events DROP CONSTRAINT appointment_events_actor_check;
ALTER TABLE appointment_events DROP COLUMN actor_customer_id;
ALTER TABLE appointment_events ALTER COLUMN actor_staff_id SET NOT NULL;
ALTER TABLE appointments DROP CONSTRAINT appointments_created_by_customer_fkey;
ALTER TABLE appointments DROP CONSTRAINT appointments_actor_check;
ALTER TABLE appointments DROP COLUMN created_by_customer;
ALTER TABLE appointments ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE staff DROP COLUMN self_bookable;
