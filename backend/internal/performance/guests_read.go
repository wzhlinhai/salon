package performance

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// ListGuestVisitsInput 是按营业日、员工查询客数的参数。
type ListGuestVisitsInput struct {
	DateFrom string     `json:"date_from" format:"date"`
	DateTo   string     `json:"date_to" format:"date"`
	StaffID  *uuid.UUID `json:"staff_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
}

// GuestVisit 是一张结账单归给员工或门店的客次份额。
type GuestVisit struct {
	ID                 uuid.UUID  `json:"id"`
	BusinessDate       string     `json:"business_date" format:"date"`
	OrderID            uuid.UUID  `json:"order_id"`
	CheckoutID         uuid.UUID  `json:"checkout_id"`
	SettingsSnapshotID uuid.UUID  `json:"settings_snapshot_id"`
	Staff              *staff.Ref `json:"staff"`
	Share              string     `json:"share" doc:"四位小数的正份额，reversal 时计负"`
	Designated         bool       `json:"designated"`
	Gender             string     `json:"gender" enum:"male,female"`
	Reversal           bool       `json:"reversal"`
	ReversesID         *uuid.UUID `json:"reverses_id" nullable:"true"`
}

// GuestVisitTotals 是完整筛选范围内原客次减冲回的客数合计。
type GuestVisitTotals struct {
	Total         string `json:"total"`
	Designated    string `json:"designated"`
	NotDesignated string `json:"not_designated"`
	Male          string `json:"male"`
	Female        string `json:"female"`
}

// GuestVisitList 是客次分页及完整筛选范围的客数合计。
type GuestVisitList struct {
	Entries []GuestVisit     `json:"entries"`
	Totals  GuestVisitTotals `json:"totals"`
}

func registerGuestVisits(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListGuestVisitsInput, GuestVisitList]{Name: "list_guest_visits", Summary: "按营业日查询客数及冲回记录", Kind: ops.Read, Permission: perm.PerformanceView, AI: ops.AIDirect, Handle: listGuestVisits})
}

func listGuestVisits(ctx context.Context, env ops.Env, in *ListGuestVisitsInput) (*GuestVisitList, error) {
	from, err := time.Parse(settings.DateLayout, in.DateFrom)
	if err != nil {
		// rule-untested: ListGuestVisitsInput.DateFrom、DateTo 的 schema format=date 在调用前用 time.DateOnly 校验
		return nil, ops.Rule("请填写有效的营业日")
	}
	to, err := time.Parse(settings.DateLayout, in.DateTo)
	if err != nil {
		// rule-untested: ListGuestVisitsInput.DateFrom、DateTo 的 schema format=date 在调用前用 time.DateOnly 校验
		return nil, ops.Rule("请填写有效的营业日")
	}
	if from.After(to) {
		return nil, ops.Rule("开始营业日不能晚于结束营业日")
	}
	if to.Sub(from) > 92*24*time.Hour {
		return nil, ops.Rule("查询范围最多 92 天")
	}
	_, self := env.Caller.ScopeFilter(perm.PerformanceView)
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	dateFrom, dateTo := pgtype.Date{Time: from, Valid: true}, pgtype.Date{Time: to, Valid: true}
	rows, err := env.Q.ListGuestVisits(ctx, dbgen.ListGuestVisitsParams{StoreID: env.Caller.StoreID, DateFrom: dateFrom, DateTo: dateTo, SelfID: self, StaffID: in.StaffID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	totals, err := env.Q.GuestVisitTotals(ctx, dbgen.GuestVisitTotalsParams{StoreID: env.Caller.StoreID, DateFrom: dateFrom, DateTo: dateTo, SelfID: self, StaffID: in.StaffID})
	if err != nil {
		return nil, err
	}
	out := &GuestVisitList{Entries: make([]GuestVisit, 0, len(rows)), Totals: GuestVisitTotals{Total: totals.Total, Designated: totals.Designated, NotDesignated: totals.NotDesignated, Male: totals.Male, Female: totals.Female}}
	for _, e := range rows {
		var ref *staff.Ref
		if e.StaffID != nil {
			ref = &staff.Ref{ID: *e.StaffID, Name: *e.StaffName}
		}
		out.Entries = append(out.Entries, GuestVisit{ID: e.ID, BusinessDate: e.BusinessDate.Time.Format(settings.DateLayout), OrderID: e.OrderID, CheckoutID: e.CheckoutID, SettingsSnapshotID: e.SettingsSnapshotID, Staff: ref, Share: e.Share, Designated: e.Designated, Gender: e.Gender, Reversal: e.ReversesID != nil, ReversesID: e.ReversesID})
	}
	return out, nil
}

// TransformSchema 声明客次归属员工引用或空值。
func (GuestVisit) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["staff"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["staff"], {Type: "null"}}}
	return s
}
