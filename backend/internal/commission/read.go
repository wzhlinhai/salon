package commission

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// ListCommissionsInput 是按营业日、员工、来源筛选提成的分页参数。
type ListCommissionsInput struct {
	DateFrom string     `json:"date_from" format:"date"`
	DateTo   string     `json:"date_to" format:"date"`
	StaffID  *uuid.UUID `json:"staff_id,omitempty"`
	Source   *string    `json:"source,omitempty" enum:"item,consume,labor,sale"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
}

// CommissionSourceTotal 是整个筛选范围内某种来源的提成净额。
type CommissionSourceTotal struct {
	Source string `json:"source" enum:"item,consume,labor,sale"`
	Amount string `json:"amount"`
}

// CommissionList 是提成分页及整个筛选范围内的净合计。
type CommissionList struct {
	Entries []Commission            `json:"entries"`
	Total   string                  `json:"total"`
	Totals  []CommissionSourceTotal `json:"totals"`
}

func registerCommissions(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListCommissionsInput, CommissionList]{Name: "list_commissions", Summary: "按营业日查询提成与解释", Kind: ops.Read, Permission: perm.PerformanceView, AI: ops.AIDirect, Handle: listCommissions})
}

func listCommissions(ctx context.Context, env ops.Env, in *ListCommissionsInput) (*CommissionList, error) {
	from, err := time.Parse(settings.DateLayout, in.DateFrom)
	if err != nil {
		// rule-untested: ListCommissionsInput 日期由 schema format=date 在调用前校验
		return nil, ops.Rule("请填写有效的营业日")
	}
	to, err := time.Parse(settings.DateLayout, in.DateTo)
	if err != nil {
		// rule-untested: ListCommissionsInput 日期由 schema format=date 在调用前校验
		return nil, ops.Rule("请填写有效的营业日")
	}
	if from.After(to) {
		return nil, ops.Rule("开始营业日不能晚于结束营业日")
	}
	if to.Sub(from) > 92*24*time.Hour {
		return nil, ops.Rule("查询范围最多 92 天")
	}
	_, self := env.Caller.ScopeFilter(perm.PerformanceView)
	staffID := in.StaffID
	if self != nil {
		staffID = self
	}
	rows, err := Commissions(ctx, env.Q, env.Caller.StoreID, from, to, staffID)
	if err != nil {
		return nil, err
	}
	// 与 list_performance 相同：本人范围和显式员工筛选取交集。
	if self != nil && in.StaffID != nil && *self != *in.StaffID {
		rows = []Commission{}
	}
	filtered := make([]Commission, 0, len(rows))
	total := money.SignedTotal{}
	sums := map[string]money.SignedTotal{}
	for _, c := range rows {
		if in.Source != nil && c.Source != *in.Source {
			continue
		}
		filtered = append(filtered, c)
		amount := c.Amount
		total = total.Add(amount)
		sums[c.Source] = sums[c.Source].Add(amount)
	}
	out := &CommissionList{Entries: []Commission{}, Total: total.Format(), Totals: []CommissionSourceTotal{}}
	for _, source := range sources {
		out.Totals = append(out.Totals, CommissionSourceTotal{Source: source, Amount: sums[source].Format()})
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	start := 0
	if in.BeforeID != nil {
		start = len(filtered)
		for i, c := range filtered {
			if c.ID == *in.BeforeID {
				start = i + 1
				break
			}
		}
	}
	for _, c := range filtered[start:] {
		if len(out.Entries) >= int(limit) {
			break
		}
		out.Entries = append(out.Entries, c)
	}
	return out, nil
}
