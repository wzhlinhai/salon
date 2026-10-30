package settings_test

import (
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"testing"
)

func TestCardRefundSettings(t *testing.T) {
	s := open(t)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.owner, "get_settings", map[string]any{})
		if setting(t, r, settings.KeyCardRefundBonus)["value"] != "forfeit" || setting(t, r, settings.KeyCardRefundFeePercent)["value"] != float64(0) {
			t.Fatal(r)
		}
	}
	for _, value := range []any{-1, 101, 1.5, "10"} {
		if r := s.change(s.owner, settings.KeyCardRefundFeePercent, value, day(0), 409); r.Detail() != "退卡手续费须为 0 到 100 的整数" {
			t.Fatal(r)
		}
	}
	if r := s.change(s.owner, settings.KeyCardRefundBonus, "bad", day(0), 409); r.Detail() != "退卡赠送金的做法无效" {
		t.Fatal(r)
	}
	s.change(s.owner, settings.KeyCardRefundBonus, "deduct_used", day(0), 200)
	s.change(s.owner, settings.KeyCardRefundFeePercent, 100, day(0), 200)
}
