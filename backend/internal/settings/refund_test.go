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
		s.change(s.owner, settings.KeyCardRefundFeePercent, value, day(0), 409)
	}
	s.change(s.owner, settings.KeyCardRefundBonus, "bad", day(0), 409)
	s.change(s.owner, settings.KeyCardRefundBonus, "deduct_used", day(0), 200)
	s.change(s.owner, settings.KeyCardRefundFeePercent, 100, day(0), 200)
}
