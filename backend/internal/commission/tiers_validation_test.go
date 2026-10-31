package commission_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func tierTable(mode string, from ...string) map[string]any {
	levels := []any{}
	for _, f := range from {
		levels = append(levels, map[string]any{"from": f, "percent": "10.00"})
	}
	return map[string]any{"mode": mode, "levels": levels}
}

func TestCommissionTierValidation(t *testing.T) {
	s := optest.OpenShop(t)
	for _, tc := range []struct {
		name, detail string
		tiers        map[string]any
		extra        map[string]any
		status       int
	}{
		{"one", "提成规则阶梯至少有两档：项目业绩", tierTable("whole", "0"), nil, 409},
		{"empty", "提成规则阶梯至少有两档：项目业绩", tierTable("whole"), nil, 409},
		{"first", "提成规则阶梯首档必须从 0 开始：项目业绩", tierTable("whole", "1", "100"), nil, 409},
		{"equal", "提成规则阶梯起点必须严格递增：项目业绩", tierTable("whole", "0", "0"), nil, 409},
		{"decreasing", "提成规则阶梯起点必须严格递增：项目业绩", tierTable("whole", "0", "100", "50"), nil, 409},
		{"percent", "提成规则格子必须且只能填写比例、固定金额或阶梯之一：项目业绩", tierTable("whole", "0", "100"), map[string]any{"percent": "10"}, 409},
		{"fixed", "提成规则格子必须且只能填写比例、固定金额或阶梯之一：项目业绩", tierTable("whole", "0", "100"), map[string]any{"fixed_amount": "1"}, 409},
		{"mode", "validation failed", tierTable("unknown", "0", "100"), nil, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell := map[string]any{"tiers": tc.tiers}
			for k, v := range tc.extra {
				cell[k] = v
			}
			sc, r := emptyScheme(), rule("item")
			r["cells"] = []any{cell}
			sc["rules"] = []any{r}
			reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": today(t, s).Format(time.DateOnly), "scheme": sc}), tc.status, tc.detail)
		})
	}
}

func TestCommissionTiersRoundTrip(t *testing.T) {
	s := optest.OpenShop(t)
	for _, mode := range []string{"whole", "progressive"} {
		tiers := tierTable(mode, "0.00", "10000.00")
		sc, r := emptyScheme(), rule("item")
		r["cells"] = []any{map[string]any{"tiers": tiers}}
		sc["rules"] = []any{r}
		saved := save(t, s, today(t, s), sc)
		for _, ch := range s.Channels() {
			got := ch.Call(t, s.MustCaller(s.Owner), "get_commission_scheme", map[string]any{"version": saved.Body["version"]})
			if got.Status != 200 || !reflect.DeepEqual(got.Body, saved.Body) {
				t.Fatal(got, saved)
			}
			actual := got.Body["scheme"].(map[string]any)["rules"].([]any)[0].(map[string]any)["cells"].([]any)[0].(map[string]any)["tiers"]
			if !reflect.DeepEqual(actual, tiers) {
				t.Fatal(actual, tiers)
			}
		}
	}
}
