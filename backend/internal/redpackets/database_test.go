package redpackets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func assertState(t *testing.T, err error, state string) {
	t.Helper()
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != state {
		t.Fatal(state, err)
	}
}
func TestRedPacketDatabaseProtections(t *testing.T) {
	s := open(t)
	p := s.packet("sold")
	for _, sql := range []string{"UPDATE red_packets SET face_value=face_value", "UPDATE red_packets SET kind=kind", "UPDATE red_packets SET price=price", "UPDATE red_packets SET expires_on=expires_on", "DELETE FROM red_packets", "UPDATE red_packet_sales SET staff_id=staff_id", "DELETE FROM red_packet_sales"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		dbtest.Rollback(t, tx)
		assertState(t, err, "42501")
	}
	base := `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,price,expires_on,business_date,issued_by,revoked_by,revoked_at,revoke_reason,order_payment_id) VALUES($1,$2,$3,$4,$5,$6,'2999-01-01','2020-01-01',$7,$8,$9,$10,$11)`
	for _, tc := range []struct {
		kind, face                     string
		price, by, at, reason, payment any
	}{
		{"other", "1", nil, nil, nil, nil, nil}, {"gift", "0", nil, nil, nil, nil, nil}, {"gift", "-1", nil, nil, nil, nil, nil},
		{"sold", "1", nil, nil, nil, nil, nil}, {"gift", "1", "1", nil, nil, nil, nil}, {"sold", "1", "0", nil, nil, nil, nil}, {"sold", "1", "-1", nil, nil, nil, nil},
		{"gift", "1", nil, s.Res.OwnerID, nil, nil, nil}, {"gift", "1", nil, nil, "2020-01-01", nil, nil}, {"gift", "1", nil, nil, nil, "reason", nil},
		{"gift", "1", nil, s.Res.OwnerID, "2020-01-01", nil, nil}, {"gift", "1", nil, s.Res.OwnerID, nil, "reason", nil}, {"gift", "1", nil, nil, "2020-01-01", "reason", nil},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), base, s.Res.TenantID, s.Res.StoreID, s.customer, tc.kind, tc.face, tc.price, s.Res.OwnerID, tc.by, tc.at, tc.reason, tc.payment)
		dbtest.Rollback(t, tx)
		assertState(t, err, "23514")
	}
	// 已用、撤回互斥以及支付关联的唯一性使用真实支付引用。
	o := s.call("open_order", map[string]any{"customer_id": s.customer, "gender": "female", "designated": false, "items": []any{}}, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var checkout, payment uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO checkouts(tenant_id,store_id,business_date,checked_out_by) VALUES($1,$2,'2020-01-01',$3) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount,red_packet_id) VALUES($1,$2,$3,$4,'red_packet',1,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, checkout, o["id"], p["id"]).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, "UPDATE red_packets SET order_payment_id=$2 WHERE id=$1", p["id"], payment)
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		sql   string
		args  []any
		state string
	}{
		{"UPDATE red_packets SET revoked_by=$1,revoked_at=clock_timestamp(),revoke_reason='reason' WHERE id=$2", []any{s.Res.OwnerID, p["id"]}, "23514"},
		{base, []any{s.Res.TenantID, s.Res.StoreID, s.customer, "gift", "1", nil, s.Res.OwnerID, nil, nil, nil, payment}, "23505"},
		{`INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount,red_packet_id) VALUES($1,$2,$3,$4,'red_packet',1,NULL)`, []any{s.Res.TenantID, s.Res.StoreID, checkout, o["id"]}, "23514"},
		{`INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount,red_packet_id) VALUES($1,$2,$3,$4,'cash',1,$5)`, []any{s.Res.TenantID, s.Res.StoreID, checkout, o["id"], p["id"]}, "23514"},
		{`INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount,red_packet_id) VALUES($1,$2,$3,$4,'red_packet',1,$5)`, []any{s.Res.TenantID, s.Res.StoreID, checkout, o["id"], uuid.New()}, "23503"},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
		dbtest.Rollback(t, tx)
		assertState(t, err, tc.state)
	}
	dbtest.MigrateDownTo(t, s.Pool, 21)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var absent bool
	if err := tx.QueryRow(context.Background(), `SELECT to_regclass('red_packets') IS NULL AND to_regclass('red_packet_sales') IS NULL AND NOT EXISTS(SELECT 1 FROM order_payments WHERE method='red_packet')`).Scan(&absent); err != nil || !absent {
		t.Fatal(absent, err)
	}
	dbtest.Rollback(t, tx)
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}
func TestRedPacketForeignKeys(t *testing.T) {
	s := open(t)
	p := s.packet("sold")
	salesStaff, _ := s.Hire("外键业绩员工", "009", "fk-sales")
	otherTenant, otherStore := dbtest.NewTenant(t, s.Pool, "外租户")
	for _, tc := range []struct {
		column string
		value  any
	}{
		{"store_id", uuid.New()}, {"store_id", otherStore}, {"customer_id", uuid.New()}, {"issued_by", uuid.New()}, {"revoked_by", uuid.New()}, {"order_payment_id", uuid.New()},
	} {
		vals := map[string]any{"tenant_id": s.Res.TenantID, "store_id": s.Res.StoreID, "customer_id": s.customer, "issued_by": s.Res.OwnerID, "revoked_by": nil, "order_payment_id": nil}
		vals[tc.column] = tc.value
		// order_payment_id 用未撤回行，避免互斥 CHECK 先挡住外键。
		revoked := "$5::uuid,NULL,NULL"
		if tc.column == "revoked_by" {
			revoked = "$5,'2020-01-01','reason'"
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), fmt.Sprintf(`INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by,revoked_by,revoked_at,revoke_reason,order_payment_id) VALUES($1,$2,$3,'gift',1,'2999-01-01','2020-01-01',$4,%s,$6)`, revoked), vals["tenant_id"], vals["store_id"], vals["customer_id"], vals["issued_by"], vals["revoked_by"], vals["order_payment_id"])
		dbtest.Rollback(t, tx)
		assertState(t, err, "23503")
	}
	for _, column := range []string{"store_id", "red_packet_id", "staff_id"} {
		vals := map[string]any{"store_id": s.Res.StoreID, "red_packet_id": p["id"], "staff_id": salesStaff}
		vals[column] = uuid.New()
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO red_packet_sales(tenant_id,store_id,red_packet_id,staff_id) VALUES($1,$2,$3,$4)`, s.Res.TenantID, vals["store_id"], vals["red_packet_id"], vals["staff_id"])
		dbtest.Rollback(t, tx)
		assertState(t, err, "23503")
	}
	tx := dbtest.AppTx(t, s.Pool, otherTenant)
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM red_packets)+(SELECT count(*) FROM red_packet_sales)").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
}

func TestRedPacketSalesUnique(t *testing.T) {
	s := open(t)
	p := s.packet("sold")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO red_packet_sales(tenant_id,store_id,red_packet_id,staff_id) VALUES($1,$2,$3,$4)`, s.Res.TenantID, s.Res.StoreID, p["id"], s.Res.OwnerID)
	dbtest.Rollback(t, tx)
	assertState(t, err, "23505")
}
