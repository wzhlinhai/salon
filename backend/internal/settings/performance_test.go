package settings_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestPerformanceSettings(t *testing.T) {
	s := open(t)
	defaults := map[string]any{"performance_split": "by_position", "performance_ratio_cash": float64(100), "performance_ratio_unionpay": float64(100), "performance_ratio_wechat": float64(100), "performance_ratio_alipay": float64(100), "performance_ratio_debt": float64(100), "performance_ratio_card_principal": float64(100), "performance_ratio_card_bonus": float64(0), "performance_ratio_red_packet": float64(0)}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.owner, "get_settings", map[string]any{})
		for k, want := range defaults {
			if setting(t, r, k)["value"] != want {
				t.Fatal(k, r)
			}
		}
	}
	for k := range defaults {
		if k == "performance_split" {
			continue
		}
		for _, v := range []any{-1, 101, 0.5} {
			r := s.change(s.owner, k, v, day(0), 409)
			d, _ := settings.Lookup(k)
			if r.Detail() != d.Label+"须为 0 到 100 的整数" {
				t.Fatal(r)
			}
		}
		s.change(s.owner, k, 37, day(0), 200)
	}
	if r := s.change(s.owner, "performance_split", "bad", day(0), 409); r.Detail() != "多人分业绩的做法无效" {
		t.Fatal(r)
	}
	s.change(s.owner, "performance_split", "equal", day(0), 200)
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		r, err := settings.Effective(ctx, q, s.Res.StoreID, at(0, "12:00"))
		if err != nil {
			return err
		}
		if r.PerformanceSplit() != "equal" {
			t.Fatal(r.PerformanceSplit())
		}
		for _, m := range []string{"cash", "unionpay", "wechat", "alipay", "debt", "red_packet", "card_principal", "card_bonus"} {
			if r.PerformanceRatio(m) != 37 {
				t.Fatal(m)
			}
		}
		defer func() {
			if recover() == nil {
				t.Error("unknown category did not panic")
			}
		}()
		r.PerformanceRatio("card")
		return nil
	})
}
