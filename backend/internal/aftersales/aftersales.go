package aftersales

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/closingguard"
	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Register 声明售后单的创建、审批、拒绝和查询操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateAftersaleInput, Aftersale]{Name: "create_aftersale", Summary: "为已结账的原单创建售后单", Kind: ops.Write, Permission: perm.AfterSalesManage, AI: ops.AIDraft, Handle: createAftersale})
	ops.Register(r, ops.Operation[ApproveAftersaleInput, Aftersale]{Name: "approve_aftersale", Summary: "审批售后单并执行退款", Kind: ops.Write, Permission: perm.AfterSalesManage, AI: ops.AINone, Handle: approveAftersale})
	ops.Register(r, ops.Operation[RejectAftersaleInput, Aftersale]{Name: "reject_aftersale", Summary: "拒绝售后单", Kind: ops.Write, Permission: perm.AfterSalesManage, AI: ops.AINone, Handle: rejectAftersale})
	ops.Register(r, ops.Operation[GetAftersaleInput, Aftersale]{Name: "get_aftersale", Summary: "查看一张售后单", Kind: ops.Read, Permission: perm.AfterSalesManage, AI: ops.AIDirect, Handle: getAftersale})
	ops.Register(r, ops.Operation[ListAftersalesInput, AftersaleList]{Name: "list_aftersales", Summary: "按营业日范围、状态或原单查看售后单", Kind: ops.Read, Permission: perm.AfterSalesManage, AI: ops.AIDirect, Handle: listAftersales})
}

func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}

func checkOrder(row dbgen.Order) error {
	switch row.Status {
	case "merged":
		return ops.Rule("单已合并，请在合并后的单上操作")
	case "voided":
		return ops.Rule("单已撤")
	case "paid":
		return nil
	default:
		return ops.Rule("只能为已结账的单创建售后单")
	}
}

func createAftersale(ctx context.Context, env ops.Env, in *CreateAftersaleInput) (*Aftersale, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, ops.Rule("请填写售后原因")
	}
	order, err := env.Q.ShareOrder(ctx, dbgen.ShareOrderParams{StoreID: env.Caller.StoreID, ID: in.OrderID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := checkOrder(order); err != nil {
		return nil, err
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, today)
	if err != nil {
		return nil, err
	}
	days := int(today.Sub(order.BusinessDate.Time).Hours() / 24)
	if days > rules.AftersaleWindowDays() {
		return nil, ops.Rule("已超过售后期限（" + strconv.Itoa(rules.AftersaleWindowDays()) + "天）")
	}
	if in.Type == "partial_refund" || in.Type == "full_refund" {
		state, err := loadRefundState(ctx, env, order.ID)
		if err != nil {
			return nil, err
		}
		if _, err := validateRefundLines(in.Type, in.RefundLines, state); err != nil {
			return nil, err
		}
		if err := checkCardsNotRefunded(ctx, env, in.RefundLines); err != nil {
			return nil, err
		}
	} else if _, err := validateRefundLines(in.Type, in.RefundLines, nil); err != nil {
		return nil, err
	}
	lines := in.RefundLines
	if lines == nil {
		lines = []RefundLine{}
	}
	raw, err := json.Marshal(lines)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateAftersale(ctx, dbgen.CreateAftersaleParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderID: order.ID,
		Type: in.Type, Reason: reason, RefundLines: raw, BusinessDate: pgtype.Date{Time: today, Valid: true}, CreatedBy: env.Caller.StaffID,
	})
	if err != nil {
		return nil, err
	}
	if _, err := env.Q.AddAftersaleEvent(ctx, dbgen.AddAftersaleEventParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AftersaleID: row.ID,
		Kind: "created", ActorID: env.Caller.StaffID, At: pgtype.Timestamptz{Time: env.Now, Valid: true},
	}); err != nil {
		return nil, err
	}
	out, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "create_aftersale", audit.AftersaleTarget(row.ID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkPending 拒绝已经审批或拒绝的售后单。
func checkPending(row dbgen.Aftersale) error {
	if row.Status == "approved" {
		return ops.Rule("售后单已审批")
	}
	if row.Status == "rejected" {
		return ops.Rule("售后单已拒绝")
	}
	return nil
}

// checkApprover 在审批方式要求另一人时拒绝创建人自己审批或拒绝。
func checkApprover(rules settings.Rules, env ops.Env, row dbgen.Aftersale) error {
	if rules.AftersaleApprovalMode() == "other" && env.Caller.StaffID == row.CreatedBy {
		return ops.Rule("售后单须由另一名员工审批")
	}
	return nil
}

func parseRefundLines(raw []byte) ([]RefundLine, error) {
	lines := []RefundLine{}
	if len(raw) == 0 {
		return lines, nil
	}
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

func approveAftersale(ctx context.Context, env ops.Env, in *ApproveAftersaleInput) (*Aftersale, error) {
	pending, err := env.Q.GetAftersale(ctx, dbgen.GetAftersaleParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	order, err := env.Q.LockOrder(ctx, dbgen.LockOrderParams{StoreID: env.Caller.StoreID, ID: pending.OrderID})
	if err != nil {
		return nil, rowError(err)
	}
	if order.Status != "paid" {
		switch order.Status {
		case "voided":
			return nil, ops.Rule("原单已撤，售后单不能生效")
		case "merged":
			// rule-untested: 合并只发生在挂单上，已结账的单不会变成 merged
			return nil, ops.Rule("原单已合并，售后单不能生效")
		default:
			// rule-untested: 售后单只能为已结账的单创建，订单状态不会退回 open
			return nil, ops.Rule("原单未结账，售后单不能生效")
		}
	}
	row, err := env.Q.LockAftersale(ctx, dbgen.LockAftersaleParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := checkPending(row); err != nil {
		return nil, err
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, today)
	if err != nil {
		return nil, err
	}
	if err := checkApprover(rules, env, row); err != nil {
		return nil, err
	}
	lines, err := parseRefundLines(row.RefundLines)
	if err != nil {
		return nil, err
	}
	deductPerf, deductComm := false, false
	var balances map[uuid.UUID]cardBalance
	var state *refundState
	var refundTotal money.Total
	if row.Type == "partial_refund" || row.Type == "full_refund" {
		if err := closingguard.Guard(ctx, env.Q, env.Caller.StoreID, today); err != nil {
			return nil, err
		}
		balances, err = lockCards(ctx, env, lines)
		if err != nil {
			return nil, err
		}
		state, err = loadRefundState(ctx, env, row.OrderID)
		if err != nil {
			return nil, err
		}
		refundTotal, err = validateRefundLines(row.Type, lines, state)
		if err != nil {
			return nil, err
		}
		deductPerf = rules.AftersaleDeductPerformance()
		if in.DeductPerformance != nil {
			deductPerf = *in.DeductPerformance
		}
		deductComm = rules.AftersaleDeductCommission()
		if in.DeductCommission != nil {
			deductComm = *in.DeductCommission
		}
		if deductComm && !deductPerf {
			return nil, ops.Rule("扣提成时必须同时扣业绩")
		}
	}
	before, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if _, err := env.Q.AddAftersaleRefund(ctx, dbgen.AddAftersaleRefundParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AftersaleID: row.ID,
			Method: line.Method, Amount: &line.Amount, CardID: line.CardID, BusinessDate: pgtype.Date{Time: today, Valid: true},
		}); err != nil {
			return nil, err
		}
		if line.Method != "card_principal" || line.CardID == nil {
			continue
		}
		balance := balances[*line.CardID]
		if _, err := cards.AddAftersaleRefund(ctx, env, *line.CardID, balance.principal, balance.bonus, line.Amount, today, row.ID); err != nil {
			return nil, err
		}
		balance.principal, err = balance.principal.Add(line.Amount)
		if err != nil {
			return nil, err
		}
		balances[*line.CardID] = balance
	}
	if deductPerf {
		if err := deductPerformance(ctx, env, row.OrderID, row.ID, today, refundTotal, state); err != nil {
			return nil, err
		}
	}
	updated, err := env.Q.ApproveAftersale(ctx, dbgen.ApproveAftersaleParams{
		StoreID: env.Caller.StoreID, ID: row.ID, ApprovedBy: &env.Caller.StaffID,
		ApprovedAt:        pgtype.Timestamptz{Time: env.Now, Valid: true},
		DeductPerformance: deductPerf, DeductCommission: deductComm,
	})
	if err != nil {
		return nil, rowError(err)
	}
	if _, err := env.Q.AddAftersaleEvent(ctx, dbgen.AddAftersaleEventParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AftersaleID: row.ID,
		Kind: "approved", ActorID: env.Caller.StaffID, At: pgtype.Timestamptz{Time: env.Now, Valid: true},
	}); err != nil {
		return nil, err
	}
	out, err := view(ctx, env, updated)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "approve_aftersale", audit.AftersaleTarget(row.ID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func rejectAftersale(ctx context.Context, env ops.Env, in *RejectAftersaleInput) (*Aftersale, error) {
	pending, err := env.Q.GetAftersale(ctx, dbgen.GetAftersaleParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	if _, err := env.Q.LockOrder(ctx, dbgen.LockOrderParams{StoreID: env.Caller.StoreID, ID: pending.OrderID}); err != nil {
		return nil, rowError(err)
	}
	row, err := env.Q.LockAftersale(ctx, dbgen.LockAftersaleParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := checkPending(row); err != nil {
		return nil, err
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, today)
	if err != nil {
		return nil, err
	}
	if err := checkApprover(rules, env, row); err != nil {
		return nil, err
	}
	var note *string
	if in.Note != nil {
		trimmed := strings.TrimSpace(*in.Note)
		if trimmed != "" {
			note = &trimmed
		}
	}
	before, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	updated, err := env.Q.RejectAftersale(ctx, dbgen.RejectAftersaleParams{
		StoreID: env.Caller.StoreID, ID: row.ID, RejectedBy: &env.Caller.StaffID,
		RejectedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}, RejectNote: note,
	})
	if err != nil {
		return nil, rowError(err)
	}
	if _, err := env.Q.AddAftersaleEvent(ctx, dbgen.AddAftersaleEventParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AftersaleID: row.ID,
		Kind: "rejected", Note: note, ActorID: env.Caller.StaffID, At: pgtype.Timestamptz{Time: env.Now, Valid: true},
	}); err != nil {
		return nil, err
	}
	out, err := view(ctx, env, updated)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "reject_aftersale", audit.AftersaleTarget(row.ID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func getAftersale(ctx context.Context, env ops.Env, in *GetAftersaleInput) (*Aftersale, error) {
	row, err := env.Q.GetAftersale(ctx, dbgen.GetAftersaleParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	return view(ctx, env, row)
}

func listAftersales(ctx context.Context, env ops.Env, in *ListAftersalesInput) (*AftersaleList, error) {
	query, err := daterange.Parse(env.Caller, perm.AfterSalesManage, in.DateFrom, in.DateTo, nil, in.Limit)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListAftersales(ctx, dbgen.ListAftersalesParams{
		StoreID: env.Caller.StoreID, DateFrom: pgtype.Date{Time: query.From, Valid: true}, DateTo: pgtype.Date{Time: query.To, Valid: true},
		Status: in.Status, OrderID: in.OrderID, BeforeID: in.BeforeID, RowLimit: query.Limit,
	})
	if err != nil {
		return nil, err
	}
	items, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	return &AftersaleList{Items: items}, nil
}
