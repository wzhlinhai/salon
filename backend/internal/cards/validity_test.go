package cards_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestValidityUsesBusinessDate(t *testing.T) {
	s := open(t)
	in := typeInput("三十天")
	in["validity_kind"] = "days_after_open"
	in["validity_days"] = 30
	ct := s.call("create_card_type", in, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, tx, `INSERT INTO setting_changes (tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES ($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2020-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	now := time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location)
	out, err := cards.OpenCardAtForTest(context.Background(), ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner)}, &cards.OpenCardInput{CustomerID: uuid.MustParse(s.customer), CardTypeID: uuid.MustParse(ct["id"].(string)), TopUpInput: cards.TopUpInput{Principal: money.Amount{}, Bonus: money.Amount{}, SalesStaffIDs: []uuid.UUID{s.Res.OwnerID}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if out.ExpiresOn == nil || *out.ExpiresOn != "2030-01-31" {
		t.Fatal(out)
	}
	var day string
	if err := tx.QueryRow(context.Background(), "SELECT business_date::text FROM card_ledger WHERE card_id=$1", out.ID).Scan(&day); err != nil || day != "2030-01-01" {
		t.Fatal(day, err)
	}
}
