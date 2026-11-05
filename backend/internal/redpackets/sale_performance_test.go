package redpackets_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
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
	s.SetNow(time.Date(2029, 12, 31, 12, 0, 0, 0, settings.Location))
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": "2030-01-01"}, 200)
	s.SetNow(time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location))
	in := s.input("sold")
	in["face_value"], in["price"] = "88", "88"
	s.call("issue_red_packet", in, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
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
	optest.RequireDraftCreated(t, r, dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries", "red_packets") == before)
}
