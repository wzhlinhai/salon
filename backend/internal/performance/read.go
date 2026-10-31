package performance

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// ListPerformanceInput 是按营业日查询业绩的参数。
type ListPerformanceInput struct {
	Kind     *string    `json:"kind,omitempty" enum:"item,consume,labor,sale"`
	DateFrom string     `json:"date_from" format:"date"`
	DateTo   string     `json:"date_to" format:"date"`
	StaffID  *uuid.UUID `json:"staff_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
}

// PerformanceEntry 是可追溯到结账单与规则快照或销售来源的业绩，冲回记录以 reversal 标明负向。
type PerformanceEntry struct {
	ID                 uuid.UUID    `json:"id"`
	Kind               string       `json:"kind" enum:"item,consume,labor,sale"`
	BusinessDate       string       `json:"business_date" format:"date"`
	OrderID            *uuid.UUID   `json:"order_id" nullable:"true" doc:"结账来源，销售业绩为空"`
	OrderItemID        *uuid.UUID   `json:"order_item_id" nullable:"true" doc:"结账来源，销售业绩为空"`
	ItemName           *string      `json:"item_name" nullable:"true" doc:"项目名称，销售业绩为空"`
	Slot               *int16       `json:"slot" nullable:"true"`
	PositionID         *uuid.UUID   `json:"position_id" nullable:"true"`
	Staff              *staff.Ref   `json:"staff"`
	Method             string       `json:"method" enum:"cash,unionpay,wechat,alipay,debt,red_packet,card_principal,card_bonus,count_paid,count_gift,labor,card_open,card_recharge,card_upgrade,red_packet_sold"`
	PaidAmount         money.Amount `json:"paid_amount" doc:"非负实收份额，reversal 为 true 时计负数"`
	Ratio              int16        `json:"ratio"`
	Amount             money.Amount `json:"amount" doc:"非负业绩金额，reversal 为 true 时计负数"`
	SettingsSnapshotID *uuid.UUID   `json:"settings_snapshot_id" nullable:"true" doc:"结账来源，销售业绩为空"`
	CheckoutID         *uuid.UUID   `json:"checkout_id" nullable:"true" doc:"结账来源，销售业绩为空"`
	CardLedgerID       *uuid.UUID   `json:"card_ledger_id" nullable:"true" doc:"开卡、充值或补差价流水"`
	RedPacketID        *uuid.UUID   `json:"red_packet_id" nullable:"true" doc:"卖红包的来源红包"`
	Purchase           *string      `json:"purchase" nullable:"true" enum:"first,repeat" doc:"销售首购或复购"`
	Reversal           bool         `json:"reversal"`
	ReversesID         *uuid.UUID   `json:"reverses_id" nullable:"true"`
}

// PerformanceTotals 是完整筛选范围内原记录减去冲回的有符号合计。
type PerformanceTotals struct {
	Kind       string `json:"kind" enum:"item,consume,labor,sale"`
	Amount     string `json:"amount" doc:"业绩净额，可为负数"`
	PaidAmount string `json:"paid_amount" doc:"实收份额净额，可为负数"`
}

// PerformanceList 是最新在前的业绩分页及整个筛选范围的合计。
type PerformanceList struct {
	Entries []PerformanceEntry  `json:"entries"`
	Totals  []PerformanceTotals `json:"totals"`
}

// Register 声明按营业日及本人或全店范围查询业绩的读操作。
func Register(r *ops.Registry) {
	registerGuestVisits(r)
	ops.Register(r, ops.Operation[ListPerformanceInput, PerformanceList]{
		Name: "list_performance", Summary: "按营业日查询业绩及冲回记录", Kind: ops.Read, Permission: perm.PerformanceView, AI: ops.AIDirect, Handle: listPerformance,
	})
}

func listPerformance(ctx context.Context, env ops.Env, in *ListPerformanceInput) (*PerformanceList, error) {
	from, err := time.Parse(settings.DateLayout, in.DateFrom)
	if err != nil {
		return nil, ops.Rule("请填写有效的营业日")
	}
	to, err := time.Parse(settings.DateLayout, in.DateTo)
	if err != nil {
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
	datesFrom, datesTo := pgtype.Date{Time: from, Valid: true}, pgtype.Date{Time: to, Valid: true}
	rows, err := env.Q.ListPerformance(ctx, dbgen.ListPerformanceParams{StoreID: env.Caller.StoreID, DateFrom: datesFrom, DateTo: datesTo, SelfID: self, StaffID: in.StaffID, Kind: in.Kind, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	totals, err := env.Q.PerformanceTotals(ctx, dbgen.PerformanceTotalsParams{StoreID: env.Caller.StoreID, DateFrom: datesFrom, DateTo: datesTo, SelfID: self, StaffID: in.StaffID, Kind: in.Kind})
	if err != nil {
		return nil, err
	}
	out := &PerformanceList{Entries: make([]PerformanceEntry, 0, len(rows)), Totals: []PerformanceTotals{{Kind: "item", Amount: "0", PaidAmount: "0"}, {Kind: "consume", Amount: "0", PaidAmount: "0"}, {Kind: "labor", Amount: "0", PaidAmount: "0"}, {Kind: "sale", Amount: "0", PaidAmount: "0"}}}
	for _, total := range totals {
		for i := range out.Totals {
			if out.Totals[i].Kind == total.Kind {
				out.Totals[i].Amount = total.Amount
				out.Totals[i].PaidAmount = total.PaidAmount
			}
		}
	}
	for _, e := range rows {
		var ref *staff.Ref
		if e.StaffID != nil {
			ref = &staff.Ref{ID: *e.StaffID, Name: *e.StaffName}
		}
		out.Entries = append(out.Entries, PerformanceEntry{ID: e.ID, Kind: e.Kind, BusinessDate: e.BusinessDate.Time.Format(settings.DateLayout), OrderID: e.OrderID, OrderItemID: e.OrderItemID, ItemName: e.ItemName, Slot: e.Slot, PositionID: e.PositionID, Staff: ref, Method: e.Method, PaidAmount: e.PaidAmount, Ratio: e.Ratio, Amount: e.Amount, SettingsSnapshotID: e.SettingsSnapshotID, CheckoutID: e.CheckoutID, CardLedgerID: e.CardLedgerID, RedPacketID: e.RedPacketID, Purchase: e.Purchase, Reversal: e.ReversesID != nil, ReversesID: e.ReversesID})
	}
	return out, nil
}

// TransformSchema 声明业绩员工引用或空值。
func (PerformanceEntry) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["staff"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["staff"], {Type: "null"}}}
	return s
}
