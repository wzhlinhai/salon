package settings_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestAIAgentSettingDefaultsAndRanges(t *testing.T) {
	s := open(t)
	r := s.Call(t, s.Owner, "get_settings", map[string]any{}, 200)
	cases := []struct {
		key   string
		def   float64
		min   float64
		max   float64
		label string
	}{
		{settings.KeyAICouponLinkWaitMinutes, 30, 5, 240, "团购券核销后多久查关联"},
		{settings.KeyAIHandoverRemindMinutes, 30, 5, 240, "交办多久没处理再提醒"},
		{settings.KeyAIHandoverManagerMarginMinutes, 15, 0, 120, "交办离时限多久交店长"},
	}
	for _, tc := range cases {
		got := setting(t, r, tc.key)
		if got["group"] != "ai" || got["kind"] != "integer" || got["label"] != tc.label {
			t.Fatalf("%s = %v", tc.key, got)
		}
		if got["value"] != tc.def || got["default"] != tc.def || got["min"] != tc.min || got["max"] != tc.max || got["is_default"] != true {
			t.Fatalf("%s = %v", tc.key, got)
		}
	}
}
