package orders_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func voidState(t *testing.T, err error, state string) {
	t.Helper()
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != state {
		t.Fatal(state, err)
	}
}
func (s *fixture) cloneRefused(table string, source any, overrides map[string]any, state string) {
	s.t.Helper()
	overrides["id"] = uuid.NewString()
	data, err := json.Marshal(overrides)
	if err != nil {
		s.t.Fatal(err)
	}
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(context.Background(), fmt.Sprintf("INSERT INTO %s SELECT (jsonb_populate_record(NULL::%s,to_jsonb(l)||$2::jsonb)).* FROM %s l WHERE id=$1", table, table, table), source, string(data))
	dbtest.Rollback(s.t, tx)
	voidState(s.t, err, state)
}
func TestVoidDatabaseConstraints(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("卡", nil), "500", "100")
	cc := s.countCard(line, "count", 4, 2)
	a := s.customerOrder(line, line)
	a = s.paid(a, cardPayment(c, "100"), countPayment(cc, firstLine(a), "100"))
	after := s.void(a)
	v := after["void"].(map[string]any)
	b := s.customerOrder(line, line)
	s.paid(b, cardPayment(c, "100"), countPayment(cc, firstLine(b), "100"))
	balance := s.consumeEntries(c)
	count := s.countEntries(cc)
	var balanceVoid, countVoid map[string]any
	for _, raw := range balance {
		l := raw.(map[string]any)
		if l["kind"] == "void" {
			balanceVoid = l
		}
	}
	for _, raw := range count {
		l := raw.(map[string]any)
		if l["kind"] == "void" {
			countVoid = l
		}
	}
	for _, tc := range []struct {
		table   string
		row     map[string]any
		consume any
	}{
		{"card_ledger", balanceVoid, balance[0].(map[string]any)["id"]},
		{"card_count_ledger", countVoid, count[0].(map[string]any)["id"]},
	} {
		s.cloneRefused(tc.table, tc.row["id"], map[string]any{"kind": "unknown", "reverses_id": nil, "order_void_id": nil}, "23514")
		base := map[string]any{"reverses_id": tc.consume}
		cases := []map[string]any{{"reverses_id": nil}, {"order_void_id": nil}, {"kind": "adjust", "order_void_id": nil}, {"kind": "adjust", "reverses_id": nil}}
		if tc.table == "card_ledger" {
			cases = append(cases, map[string]any{"principal_delta": "-1", "principal_before": "1", "principal_after": "0", "bonus_delta": "2", "bonus_before": "0", "bonus_after": "2"}, map[string]any{"principal_delta": "2", "principal_before": "0", "principal_after": "2", "bonus_delta": "-1", "bonus_before": "1", "bonus_after": "0"}, map[string]any{"principal_delta": "0", "principal_after": tc.row["principal_before"], "bonus_delta": "0", "bonus_after": tc.row["bonus_before"]})
		} else {
			cases = append(cases, map[string]any{"paid_delta": 2, "paid_after": tc.row["paid_before"].(float64) + 2, "gift_delta": 0, "gift_after": tc.row["gift_before"]}, map[string]any{"paid_before": 1, "paid_delta": -1, "paid_after": 0, "gift_before": 0, "gift_delta": 2, "gift_after": 2}, map[string]any{"paid_before": 0, "paid_delta": 2, "paid_after": 2, "gift_before": 1, "gift_delta": -1, "gift_after": 0})
		}
		for _, override := range cases {
			for k, value := range base {
				if _, ok := override[k]; !ok {
					override[k] = value
				}
			}
			s.cloneRefused(tc.table, tc.row["id"], override, "23514")
		}
		s.cloneRefused(tc.table, tc.row["id"], map[string]any{}, "23505")
		for _, column := range []string{"reverses_id", "order_void_id"} {
			overrides := map[string]any{"reverses_id": tc.consume}
			overrides[column] = uuid.NewString()
			s.cloneRefused(tc.table, tc.row["id"], overrides, "23503")
		}
	}
	for _, reason := range []string{"", "   ", strings.Repeat("撤", 201)} {
		s.cloneRefused("order_voids", v["id"], map[string]any{"order_id": b["id"], "reason": reason}, "23514")
	}
	s.cloneRefused("order_voids", v["id"], map[string]any{}, "23505")
	for _, column := range []string{"store_id", "order_id", "voided_by"} {
		overrides := map[string]any{"order_id": b["id"]}
		overrides[column] = uuid.NewString()
		s.cloneRefused("order_voids", v["id"], overrides, "23503")
	}
	for _, table := range []string{"order_voids", "card_ledger", "card_count_ledger"} {
		for _, sql := range []string{"UPDATE " + table + " SET id=id", "DELETE FROM " + table} {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), sql)
			dbtest.Rollback(t, tx)
			voidState(t, err, "42501")
		}
	}
	// voided 仍须保留 checkout_id；未知状态拒绝。
	openOrder := s.customerOrder()
	for _, sql := range []string{"UPDATE orders SET status='voided' WHERE id=$1", "UPDATE orders SET status='unknown' WHERE id=$1"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql, openOrder["id"])
		dbtest.Rollback(t, tx)
		voidState(t, err, "23514")
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), "UPDATE orders SET checkout_id=NULL WHERE id=$1", a["id"])
	dbtest.Rollback(t, tx)
	voidState(t, err, "23514")
}
func TestVoidMigrationDown(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("卡", nil), "100", "0")
	cc := s.countCard(line, "count", 1, 0)
	a := s.customerOrder(line, line)
	a = s.paid(a, cardPayment(c, "100"), countPayment(cc, firstLine(a), "100"))
	s.void(a)
	dbtest.MigrateDownTo(t, s.Pool, 23)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ok bool
	err := tx.QueryRow(context.Background(), `SELECT to_regclass('order_voids') IS NULL AND NOT EXISTS(SELECT 1 FROM card_ledger WHERE kind='void') AND NOT EXISTS(SELECT 1 FROM card_count_ledger WHERE kind='void') AND (SELECT status='paid' AND checkout_id IS NOT NULL FROM orders WHERE id=$1) AND EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='order_payments_red_packet')`, a["id"]).Scan(&ok)
	dbtest.Rollback(t, tx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}
func TestVoidMigrationDownRefusesReusedPacket(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	p := s.redPacket(s.customer, "100")
	s.void(s.paid(s.customerOrder(line), packetPayment(p, "100")))
	s.paid(s.customerOrder(line), packetPayment(p, "100"))
	err := db.MigrateDown(context.Background(), s.Pool)
	if err == nil || !strings.Contains(err.Error(), "cannot downgrade 00024: a restored red packet has multiple historical payments") {
		t.Fatal(err)
	}
}
func TestVoidExpiredPacket(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	day := settings.Date(time.Now()).AddDate(0, 0, -2)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var packet uuid.UUID
	err := tx.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',100,$4,$4,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.customer, day, s.Res.OwnerID).Scan(&packet)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	o := s.customerOrder(line)
	s.checkoutAtForVoid(o, day, packetPayment(map[string]any{"id": packet.String()}, "100"))
	s.void(o)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": s.customer})
		if r.Status != 200 || r.Body["items"].([]any)[0].(map[string]any)["status"] != "expired" {
			t.Fatal(ch.Name, r)
		}
	}
}
