-- +goose Up
ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card','order','red_packet','debt')) = (customer_id IS NOT NULL));

UPDATE audit_log
SET target_kind = CASE WHEN action IN ('register_historical_debt','repay_debt') THEN 'debt' ELSE 'red_packet' END,
    target_id = COALESCE(after->>'id', before->>'id')::uuid
WHERE target_kind = 'customer'
  AND action IN ('issue_red_packet','revoke_red_packet','register_historical_debt','repay_debt','void_order');

-- +goose Down
UPDATE audit_log SET target_kind = 'customer', target_id = customer_id
WHERE target_kind IN ('red_packet','debt');

ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card','order')) = (customer_id IS NOT NULL));
