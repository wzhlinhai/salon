package settings_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestCountPaymentSettings(t *testing.T) {
	s := open(t)
	def, ok := settings.Lookup(settings.KeyCardCountOrder)
	if !ok || def.Label != "扣次顺序" || !reflect.DeepEqual(def.Options, []settings.ChoiceOption{{Value: "paid_first", Label: "先扣购买次数"}, {Value: "gift_first", Label: "先扣赠送次数"}}) {
		t.Fatal(def)
	}
	for _, ch := range s.Channels() {
		got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyCardCountOrder)
		if got["value"] != "paid_first" || got["default"] != "paid_first" {
			t.Fatal(got)
		}
	}
	if r := s.change(s.owner, settings.KeyCardCountOrder, "invalid", day(0), 409); r.Detail() != "扣次顺序的做法无效" {
		t.Fatal(r)
	}
	s.change(s.owner, settings.KeyCardCountOrder, "gift_first", day(0), 200)
	for _, ch := range s.Channels() {
		got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyCardCountOrder)
		if got["value"] != "gift_first" || got["is_default"] != false {
			t.Fatal(got)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	snap, err := settings.TakeSnapshot(context.Background(), q, s.Res.TenantID, s.Res.StoreID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rules, err := settings.LoadSnapshot(context.Background(), q, s.Res.StoreID, snap.ID)
	if err != nil || rules.CardCountOrder() != "gift_first" {
		t.Fatal(rules, err)
	}
	dbtest.Rollback(t, tx)
}
