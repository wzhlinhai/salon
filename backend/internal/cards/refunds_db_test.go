package cards_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func refundState(t *testing.T, err error, want string) {
	t.Helper()
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != want {
		t.Fatal(want, err)
	}
}
func (s *fixture) refundClone(table string, source any, overrides map[string]any, want string) {
	s.t.Helper()
	if _, exists := overrides["id"]; !exists {
		overrides["id"] = uuid.NewString()
	}
	data, err := json.Marshal(overrides)
	if err != nil {
		s.t.Fatal(err)
	}
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(context.Background(), fmt.Sprintf("INSERT INTO %s SELECT (jsonb_populate_record(NULL::%s,to_jsonb(l)||$2::jsonb)).* FROM %s l WHERE id=$1", table, table, table), source, string(data))
	dbtest.Rollback(s.t, tx)
	refundState(s.t, err, want)
}
func TestRefundDatabase(t *testing.T) {
	s := open(t)
	c, _, _ := s.countCard("package")
	out := s.refund(c["id"], "1357.96")
	other := s.call("open_card", s.opening(), 200)
	for _, override := range []map[string]any{
		{"bonus_policy": "deduct_used", "bonus_deducted": "1357.97", "fee": "0", "amount": "0"},
		{"method": "invalid"}, {"bonus_policy": "invalid"}, {"bonus_deducted": "1"},
		{"fee_percent": -1}, {"fee_percent": 101}, {"fee": "0.01"}, {"amount": "0.01"},
		{"principal": "-1"}, {"bonus": "-1"}, {"counts_amount": "-1"}, {"bonus_deducted": "-1"}, {"fee": "-1"}, {"amount": "-1"},
	} {
		override["card_id"] = other["id"]
		s.refundClone("card_refunds", out["id"], override, "23514")
	}
	s.refundClone("card_refunds", out["id"], map[string]any{}, "23505")
	for _, col := range []string{"store_id", "card_id", "refunded_by"} {
		override := map[string]any{"card_id": other["id"], col: uuid.NewString()}
		s.refundClone("card_refunds", out["id"], override, "23503")
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var balance, count uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT id FROM card_ledger WHERE refund_id=$1", out["id"]).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT id FROM card_count_ledger WHERE refund_id=$1 LIMIT 1", out["id"]).Scan(&count); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	for _, row := range []struct {
		table string
		id    uuid.UUID
	}{{"card_ledger", balance}, {"card_count_ledger", count}} {
		s.refundClone(row.table, row.id, map[string]any{"kind": "invalid", "refund_id": nil}, "23514")
		s.refundClone(row.table, row.id, map[string]any{"refund_id": nil}, "23514")
		s.refundClone(row.table, row.id, map[string]any{"kind": "adjust"}, "23514")
		s.refundClone(row.table, row.id, map[string]any{"refund_id": uuid.NewString()}, "23503")
	}
	s.refundClone("card_ledger", balance, map[string]any{}, "23505")
	for _, field := range []string{"principal", "bonus"} {
		s.refundClone("card_ledger", balance, map[string]any{field + "_after": "1", field + "_delta": map[string]string{"principal": "-1233.56", "bonus": "0.50"}[field]}, "23514")
	}
	for _, field := range []string{"paid", "gift"} {
		s.refundClone("card_count_ledger", count, map[string]any{field + "_after": 1, field + "_delta": map[string]int{"paid": -9, "gift": -2}[field]}, "23514")
	}
	for _, sql := range []string{"UPDATE card_refunds SET amount=amount", "DELETE FROM card_refunds"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		dbtest.Rollback(t, tx)
		refundState(t, err, "42501")
	}
	for _, status := range []any{"invalid", nil} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE cards SET status=$2 WHERE id=$1", c["id"], status)
		dbtest.Rollback(t, tx)
		want := "23514"
		if status == nil {
			want = "23502"
		}
		refundState(t, err, want)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var identity bool
	err := tx.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='card_refunds'::regclass AND pg_get_constraintdef(oid)='UNIQUE (tenant_id, id)')`).Scan(&identity)
	dbtest.Rollback(t, tx)
	if err != nil || !identity {
		t.Fatal(identity, err)
	}
}
func TestRefundMigrationDown(t *testing.T) {
	s := open(t)
	c, _, _ := s.countCard("package")
	s.refund(c["id"], "1357.96")
	dbtest.MigrateDownTo(t, s.Pool, 25)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var gone bool
	err := tx.QueryRow(context.Background(), `SELECT to_regclass('card_refunds') IS NULL AND NOT EXISTS(SELECT 1 FROM card_ledger WHERE kind='refund') AND NOT EXISTS(SELECT 1 FROM card_count_ledger WHERE kind='refund') AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='cards' AND column_name='status')`).Scan(&gone)
	dbtest.Rollback(t, tx)
	if err != nil || !gone {
		t.Fatal(gone, err)
	}
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
}

func TestRefundDatabaseSchema(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for _, tc := range []struct {
		table string
		parts []string
	}{
		{"card_refunds", []string{"PRIMARY KEY (id)", "UNIQUE (card_id)", "UNIQUE (tenant_id, id)", "(principal >=", "(bonus >=", "(counts_amount >=", "(bonus_deducted >=", "(fee >=", "(amount >=", "fee_percent >= 0", "fee_percent <= 100", "bonus_policy = 'deduct_used'", "fee = round", "bonus_deducted <= (principal + counts_amount)", "amount = (((principal + counts_amount)", "'forfeit'::text", "'deduct_used'::text", "'cash'::text", "'unionpay'::text", "'wechat'::text", "'alipay'::text"}},
		{"cards", []string{"'active'::text", "'refunded'::text"}},
		{"card_ledger", []string{"UNIQUE (refund_id)", "kind = 'refund'::text", "refund_id IS NOT NULL", "principal_after =", "bonus_after ="}},
		{"card_count_ledger", []string{"kind = 'refund'::text", "refund_id IS NOT NULL", "paid_after =", "gift_after ="}},
	} {
		var defs string
		if err := tx.QueryRow(context.Background(), "SELECT string_agg(pg_get_constraintdef(oid), E'\n') FROM pg_constraint WHERE conrelid=$1::regclass", tc.table).Scan(&defs); err != nil {
			t.Fatal(err)
		}
		for _, part := range tc.parts {
			if !strings.Contains(defs, part) {
				t.Errorf("%s missing %s: %s", tc.table, part, defs)
			}
		}
	}
	var required bool
	if err := tx.QueryRow(context.Background(), `SELECT bool_and(attnotnull) AND count(*)=16 FROM pg_attribute WHERE attrelid='card_refunds'::regclass AND attnum>0 AND NOT attisdropped`).Scan(&required); err != nil || !required {
		t.Fatal(required, err)
	}
	dbtest.Rollback(t, tx)
}

func TestRefundRequiredColumns(t *testing.T) {
	s := open(t)
	c := s.call("open_card", s.opening(), 200)
	out := s.refund(c["id"], "1234.56")
	other := s.call("open_card", s.opening(), 200)
	for _, col := range []string{"tenant_id", "store_id", "id", "card_id", "method", "principal", "bonus", "counts_amount", "bonus_policy", "bonus_deducted", "fee_percent", "fee", "amount", "business_date", "refunded_by", "at"} {
		override := map[string]any{"card_id": other["id"], col: nil}
		want := "23502"
		if col == "tenant_id" {
			want = "42501"
		}
		s.refundClone("card_refunds", out["id"], override, want)
	}
}

func TestRefundTenantIsolation(t *testing.T) {
	s := open(t)
	c := s.call("open_card", s.opening(), 200)
	out := s.refund(c["id"], "1234.56")
	other := s.call("open_card", s.opening(), 200)
	foreign := dbtest.AppTx(t, s.Pool, uuid.New())
	var count int
	err := foreign.QueryRow(context.Background(), "SELECT count(*) FROM card_refunds").Scan(&count)
	dbtest.Rollback(t, foreign)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	s.refundClone("card_refunds", out["id"], map[string]any{"tenant_id": uuid.NewString(), "card_id": other["id"]}, "42501")
}
