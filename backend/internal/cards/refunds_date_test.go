package cards_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestRefundBusinessDate(t *testing.T) {
	s := open(t)
	c := s.call("open_card", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES ($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2020-01-01',$3),($1,$2,'card_refund_fee_percent','10',false,'0',true,'2030-01-01',$3),($1,$2,'card_refund_fee_percent','100',false,'10',false,'2030-01-02',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	cards.RegisterRefundAtForTest(s.Registry, func() time.Time { return time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location) })
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "test_preview_card_refund", map[string]any{"card_id": c["id"]})
		if r.Status != 200 || r.Body["fee_percent"] != float64(10) || r.Body["fee"] != "123.46" || r.Body["amount"] != "1111.10" {
			t.Fatal(ch.Name, r)
		}
	}
	r := s.HTTP(t, new(s.MustCaller(s.Owner)), "test_refund_card", map[string]any{"card_id": c["id"], "method": "cash", "amount": "1111.10"})
	if r.Status != 200 || r.Body["business_date"] != "2030-01-01" || r.Body["fee_percent"] != float64(10) || r.Body["fee"] != "123.46" {
		t.Fatal(r)
	}
}

func TestRefundCandidateList(t *testing.T) {
	s := open(t)
	c := s.call("open_card", s.opening(), 200)
	s.refund(c["id"], "1234.56")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	ids, err := cards.SelectionCandidates(context.Background(), ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner)}, uuid.MustParse(s.customer), nil)
	dbtest.Rollback(t, tx)
	if err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
}
