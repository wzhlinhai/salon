package settings_test

import (
	"context"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"testing"
	"time"
)

func TestCardPaymentSettings(t *testing.T) {
	s := open(t)
	for _, tc := range []struct{ key, def, value, wantDetail string }{
		{settings.KeyCardDeductionOrder, "principal_first", "bonus_first", "扣款顺序的做法无效"},
		{settings.KeyOthersCardPayment, "not_allowed", "allowed", "用别人的卡的做法无效"},
	} {
		for _, ch := range s.Channels() {
			got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), tc.key)
			if got["value"] != tc.def || got["default"] != tc.def {
				t.Fatal(got)
			}
		}
		if r := s.change(s.owner, tc.key, "invalid", day(0), 409); r.Detail() != tc.wantDetail {
			t.Fatal(r)
		}
		s.change(s.owner, tc.key, tc.value, day(0), 200)
		for _, ch := range s.Channels() {
			got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), tc.key)
			if got["value"] != tc.value || got["is_default"] != false {
				t.Fatal(got)
			}
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	snapshot, err := settings.TakeSnapshot(context.Background(), q, s.Res.TenantID, s.Res.StoreID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rules, err := settings.LoadSnapshot(context.Background(), q, s.Res.StoreID, snapshot.ID)
	if err != nil || rules.CardDeductionOrder() != "bonus_first" || rules.OthersCardPayment() != "allowed" {
		t.Fatal(rules, err)
	}
	dbtest.Rollback(t, tx)
}
