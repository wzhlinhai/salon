package settings_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestCommissionSplitSetting(t *testing.T) {
	s := open(t)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			got := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyCommissionSplit)
			want := []settings.ChoiceOption{{Value: "same", Label: "跟业绩分法一样"}, {Value: "equal", Label: "均分"}, {Value: "by_position", Label: "按岗位比例"}}
			if got["value"] != "same" || got["default"] != "same" || got["label"] != "提成按什么分" || got["hint"] != "只在多人分业绩选「每人各记全额」时起作用；其他分法下提成跟业绩一样；结账时固定，之后改设置不影响已结账的单；固定金额提成按人计，不分摊" {
				t.Fatal(got)
			}
			options := []settings.ChoiceOption{}
			for _, raw := range got["options"].([]any) {
				option := raw.(map[string]any)
				options = append(options, settings.ChoiceOption{Value: option["value"].(string), Label: option["label"].(string)})
			}
			if !reflect.DeepEqual(options, want) {
				t.Fatal(options)
			}
		})
	}
	bad := s.change(s.owner, settings.KeyCommissionSplit, "bad", day(0), 409)
	if bad.Detail() != "提成按什么分的做法无效" {
		t.Fatal(bad)
	}
	s.change(s.owner, settings.KeyCommissionSplit, "equal", day(0), 200)
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		rules, err := settings.Effective(ctx, q, s.Res.StoreID, at(0, "12:00"))
		if err != nil {
			return err
		}
		if rules.CommissionSplit() != "equal" {
			t.Fatal(rules.CommissionSplit())
		}
		return nil
	})
}
