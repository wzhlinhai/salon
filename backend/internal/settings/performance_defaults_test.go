package settings

import (
	"encoding/json"
	"testing"
)

func TestOldPerformanceSnapshotDefaults(t *testing.T) {
	r := rulesFrom(map[string]json.RawMessage{"member_price_rule": json.RawMessage(`"member_first"`)})
	if r.PerformanceSplit() != "by_position" {
		t.Fatal(r.PerformanceSplit())
	}
	for _, m := range []string{"cash", "unionpay", "wechat", "alipay", "debt", "red_packet", "card_principal", "card_bonus", "count_paid", "count_gift"} {
		want := 100
		if m == "red_packet" || m == "card_bonus" || m == "count_gift" {
			want = 0
		}
		if r.PerformanceRatio(m) != want {
			t.Fatal(m, r.PerformanceRatio(m))
		}
	}
}
