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

func TestCardSelectionSettings(t *testing.T) {
	s := open(t)
	def, ok := settings.Lookup(settings.KeyCardSelection)
	if !ok || def.Label != "选卡规则" || !reflect.DeepEqual(def.Options, []settings.ChoiceOption{{Value: "manual", Label: "手动选卡"}, {Value: "by_discount", Label: "按折扣率"}, {Value: "by_balance", Label: "按余额"}}) {
		t.Fatal(def)
	}
	for _, ch := range s.Channels() {
		got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyCardSelection)
		if got["value"] != "manual" || got["default"] != "manual" {
			t.Fatal(got)
		}
	}
	if r := s.change(s.owner, settings.KeyCardSelection, "invalid", day(0), 409); r.Detail() != "选卡规则的做法无效" {
		t.Fatal(r)
	}
	s.change(s.owner, settings.KeyCardSelection, "by_balance", day(0), 200)
	for _, ch := range s.Channels() {
		got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyCardSelection)
		if got["value"] != "by_balance" || got["is_default"] != false {
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
	if err != nil || rules.CardSelection() != "by_balance" {
		t.Fatal(rules, err)
	}
	dbtest.Rollback(t, tx)
}
