package commission

import (
	"context"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
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
	q, err := daterange.Parse(env.Caller, perm.PerformanceView, in.DateFrom, in.DateTo, in.StaffID, in.Limit)
	if err != nil {
		return nil, err
	}
	if q.Empty {
		return Page(nil, in.Source, q.Limit, in.BeforeID), nil
	}
	rows, err := Commissions(ctx, env.Q, env.Caller.StoreID, q.From, q.To, q.StaffID)
	if err != nil {
		return nil, err
	}
	return Page(rows, in.Source, q.Limit, in.BeforeID), nil
}

// Page 对提成按来源筛选、汇总，并按游标分页。
func Page(rows []Commission, source *string, limit int32, beforeID *uuid.UUID) *CommissionList {
	filtered := make([]Commission, 0, len(rows))
	total := money.SignedTotal{}
	sums := map[string]money.SignedTotal{}
	for _, c := range rows {
		if source != nil && c.Source != *source {
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
	if limit == 0 {
		limit = 50
	}
	start := 0
	if beforeID != nil {
		start = len(filtered)
		for i, c := range filtered {
			if c.ID == *beforeID {
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
	return out
}
