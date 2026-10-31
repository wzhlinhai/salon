package commission

import (
	"encoding/json"
	"slices"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

var sources = []string{"item", "consume", "labor", "sale"}

func validate(s *CommissionScheme) error {
	rules := map[string]bool{}
	for _, r := range s.Rules {
		if rules[r.Source] {
			return ops.Rule("同一业绩来源只能有一条提成规则：" + sourceName(r.Source))
		}
		rules[r.Source] = true
		if r.Source != "item" && r.Base != "performance" {
			return ops.Rule("提成规则的计提基数无效：" + sourceName(r.Source))
		}
		if r.Source == "sale" && (r.ByPosition || r.ByDesignated || r.ByMember) {
			return ops.Rule("销售业绩不能按岗位、指定或会员区分")
		}
		keys := map[string]bool{}
		for _, c := range r.Cells {
			if (c.PositionID != nil) != r.ByPosition || (c.Designated != nil) != r.ByDesignated || (c.Member != nil) != r.ByMember {
				return ops.Rule("提成规则格子的岗位、指定、会员必须与区分开关一致：" + sourceName(r.Source))
			}
			key, err := json.Marshal([]any{c.PositionID, c.Designated, c.Member})
			if err != nil {
				return err
			}
			if keys[string(key)] {
				return ops.Rule("提成规则有重复格子：" + sourceName(r.Source))
			}
			keys[string(key)] = true
			if (c.Percent == nil) == (c.FixedAmount == nil) {
				return ops.Rule("提成规则格子必须且只能填写比例或固定金额之一：" + sourceName(r.Source))
			}
		}
	}
	keys := map[string]bool{}
	for _, o := range s.Overrides {
		if !rules[o.Source] {
			return ops.Rule("覆盖比例的业绩来源必须有提成规则：" + sourceName(o.Source))
		}
		if o.StaffID == nil && o.ItemID == nil {
			return ops.Rule("覆盖比例至少指定员工或项目：" + sourceName(o.Source))
		}
		if o.Source == "sale" && o.ItemID != nil {
			return ops.Rule("销售业绩不能设置项目覆盖比例")
		}
		if (o.Percent == nil) == (o.FixedAmount == nil) {
			return ops.Rule("覆盖比例必须且只能填写比例或固定金额之一：" + sourceName(o.Source))
		}
		key, err := json.Marshal([]any{o.Source, o.StaffID, o.ItemID})
		if err != nil {
			return err
		}
		if keys[string(key)] {
			return ops.Rule("覆盖比例的来源、员工、项目组合重复：" + sourceName(o.Source))
		}
		keys[string(key)] = true
	}
	excluded := map[uuid.UUID]bool{}
	for _, id := range s.ExcludedStaff {
		if excluded[id] {
			return ops.Rule("不计提成的员工不能重复")
		}
		excluded[id] = true
	}
	slices.SortStableFunc(s.Rules, func(a, b Rule) int { return slices.Index(sources, a.Source) - slices.Index(sources, b.Source) })
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	if s.Overrides == nil {
		s.Overrides = []Override{}
	}
	if s.ExcludedStaff == nil {
		s.ExcludedStaff = []uuid.UUID{}
	}
	for i := range s.Rules {
		if s.Rules[i].Cells == nil {
			s.Rules[i].Cells = []Cell{}
		}
	}
	return nil
}

func sourceName(source string) string {
	return map[string]string{"item": "项目业绩", "consume": "耗卡业绩", "labor": "手工费", "sale": "销售业绩"}[source]
}
