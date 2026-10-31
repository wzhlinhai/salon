package redpackets_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestRedPacketSale(t *testing.T) {
	s := open(t)
	second, _ := s.Hire("乙", "002", "sale2")
	in := s.input("sold")
	in["price"], in["sales_staff_ids"] = "88", []uuid.UUID{second, s.Res.OwnerID}
	p := s.call("issue_red_packet", in, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ok bool
	if err := tx.QueryRow(context.Background(), `SELECT count(*)=2 AND sum(e.paid_amount)=88 AND bool_and(e.kind='sale' AND e.method='red_packet_sold' AND e.purchase='repeat' AND e.ratio=100 AND e.amount=e.paid_amount AND e.amount=44 AND e.card_ledger_id IS NULL AND e.business_date=p.business_date AND e.order_id IS NULL AND e.slot IS NULL) AND (array_agg(e.staff_id ORDER BY e.id))[1]=$2 AND (array_agg(e.staff_id ORDER BY e.id))[2]=$3 FROM performance_entries e JOIN red_packets p ON p.id=e.red_packet_id WHERE p.id=$1`, p["id"], second, s.Res.OwnerID).Scan(&ok); err != nil || !ok {
		t.Fatal(ok, err)
	}
	dbtest.Rollback(t, tx)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries")
	s.packet("gift")
	s.call("revoke_red_packet", revoke(p), 200)
	if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries") != before {
		t.Fatal("gift or revoke changed sale records")
	}
}

func TestRedPacketSaleBusinessDate(t *testing.T) {
	s := open(t)
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2020-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	var price money.Amount
	if err := price.Scan("88"); err != nil {
		t.Fatal(err)
	}
	err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := redpackets.IssueRedPacketAtForTest(context.Background(), ops.Env{Q: q, Caller: caller}, &redpackets.IssueRedPacketInput{CustomerID: s.customer, Kind: "sold", FaceValue: price, Price: &price, ExpiresOn: "2999-01-01", SalesStaffIDs: []uuid.UUID{s.Res.OwnerID}}, time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ok bool
	if err := tx.QueryRow(context.Background(), `SELECT count(*)=1 AND bool_and(e.business_date=p.business_date AND e.business_date='2030-01-01') FROM performance_entries e JOIN red_packets p ON p.id=e.red_packet_id`).Scan(&ok); err != nil || !ok {
		t.Fatal(ok, err)
	}
	dbtest.Rollback(t, tx)
}

func TestRedPacketSaleAI(t *testing.T) {
	s := open(t)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries", "red_packets")
	r := s.AI(t, s.MustCaller(s.Owner), "issue_red_packet", s.input("sold"))
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) {
		t.Fatal(r)
	}
	if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries", "red_packets") != before {
		t.Fatal("AI wrote sale")
	}
}
