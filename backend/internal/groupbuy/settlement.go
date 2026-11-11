package groupbuy

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerSettlements(r *ops.Registry) {
	ops.Register(r, ops.Operation[RecordSettlementInput, Settlement]{Name: "record_groupbuy_settlement", Summary: "手工录入团购平台某月结算额", Kind: ops.Write, Permission: perm.ClosingDaily, AI: ops.AIDraft, Handle: recordSettlement})
	ops.Register(r, ops.Operation[GroupbuyMonthInput, GroupbuyMonth]{Name: "get_groupbuy_month", Summary: "按平台和结算月查看团购核销分类、平台购买价合计、结算额和差额", Kind: ops.Read, Permission: perm.ReportView, AI: ops.AIDirect, Handle: getGroupbuyMonth})
}

func recordSettlement(ctx context.Context, env ops.Env, in *RecordSettlementInput) (*Settlement, error) {
	platform := strings.TrimSpace(in.Platform)
	note := strings.TrimSpace(in.Note)
	if platform == "" {
		return nil, ops.Rule("团购平台不能为空")
	}
	month, err := payrollmonth.Parse(in.Month)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateGroupbuySettlement(ctx, dbgen.CreateGroupbuySettlementParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Platform: platform, Month: month.String(), Amount: in.Amount, Note: note, EnteredBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	staff, err := env.Q.GetStaff(ctx, row.EnteredBy)
	if err != nil {
		return nil, err
	}
	out := settlementView(row, staff.Name)
	if err := audit.Record(ctx, env, "record_groupbuy_settlement", audit.GroupbuySettlementTarget(row.ID), nil, out); err != nil {
		return nil, err
	}
	return &out, nil
}

func settlementView(row dbgen.GroupbuySettlement, enteredByName string) Settlement {
	return Settlement{ID: row.ID, Platform: row.Platform, Month: row.Month, Amount: row.Amount, Note: row.Note, EnteredBy: row.EnteredBy, EnteredByName: enteredByName, CreatedAt: row.CreatedAt.Time}
}

func getGroupbuyMonth(ctx context.Context, env ops.Env, in *GroupbuyMonthInput) (*GroupbuyMonth, error) {
	platform := strings.TrimSpace(in.Platform)
	if platform == "" {
		return nil, ops.Rule("团购平台不能为空")
	}
	month, err := payrollmonth.Parse(in.Month)
	if err != nil {
		return nil, err
	}
	first := month.First()
	from := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, settings.Location).AddDate(0, 0, -2)
	to := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, settings.Location).AddDate(0, 1, 2)
	rows, err := env.Q.ListMonthRedemptions(ctx, dbgen.ListMonthRedemptionsParams{StoreID: env.Caller.StoreID, Platform: platform, FromTime: pgtype.Timestamptz{Time: from, Valid: true}, ToTime: pgtype.Timestamptz{Time: to, Valid: true}})
	if err != nil {
		return nil, err
	}
	times := []time.Time{}
	for _, row := range rows {
		if row.RedeemedAt.Valid {
			times = append(times, row.RedeemedAt.Time)
		}
	}
	days, err := settings.BusinessDates(ctx, env.Q, env.Caller.StoreID, times)
	if err != nil {
		return nil, err
	}
	out := &GroupbuyMonth{Platform: platform, Month: month.String(), History: []Settlement{}}
	purchase := money.Total{}
	linkedPaidPurchase := money.Total{}
	linkedOpenPurchase := money.Total{}
	unlinkedPurchase := money.Total{}
	day := 0
	for _, row := range rows {
		if !row.RedeemedAt.Valid {
			continue
		}
		current := days[day]
		day++
		if payrollmonth.Of(current) != month {
			continue
		}
		if row.Status == "revoked" {
			continue
		}
		out.RedeemedCount++
		if row.PurchasePrice != nil {
			purchase = purchase.AddProduct(*row.PurchasePrice, 1)
		}
		linked := row.OrderID != nil && row.OrderStatus != nil
		switch {
		case linked && *row.OrderStatus == "paid":
			out.LinkedPaid.Count++
			if row.PurchasePrice != nil {
				linkedPaidPurchase = linkedPaidPurchase.AddProduct(*row.PurchasePrice, 1)
			}
		case linked && *row.OrderStatus == "open":
			out.LinkedOpen.Count++
			if row.PurchasePrice != nil {
				linkedOpenPurchase = linkedOpenPurchase.AddProduct(*row.PurchasePrice, 1)
			}
		default:
			if row.PurchasePrice != nil {
				out.Unlinked.Count++
				unlinkedPurchase = unlinkedPurchase.AddProduct(*row.PurchasePrice, 1)
			} else {
				out.UnlinkedNoDeal++
			}
		}
	}
	out.LinkedPaid.PurchaseTotal = totalAmount(linkedPaidPurchase)
	out.LinkedOpen.PurchaseTotal = totalAmount(linkedOpenPurchase)
	out.Unlinked.PurchaseTotal = totalAmount(unlinkedPurchase)
	out.PurchaseTotal = totalAmount(purchase)
	settlements, err := env.Q.ListGroupbuySettlements(ctx, dbgen.ListGroupbuySettlementsParams{StoreID: env.Caller.StoreID, Platform: platform, Month: month.String()})
	if err != nil {
		return nil, err
	}
	for _, row := range settlements {
		out.History = append(out.History, Settlement{ID: row.ID, Platform: row.Platform, Month: row.Month, Amount: row.Amount, Note: row.Note, EnteredBy: row.EnteredBy, EnteredByName: row.EnteredByName, CreatedAt: row.CreatedAt.Time})
	}
	if len(out.History) > 0 {
		latest := out.History[0]
		out.Settlement = &latest
		difference := latest.Amount.Sub(out.PurchaseTotal)
		out.Difference = &difference
	}
	return out, nil
}

func totalAmount(total money.Total) money.Amount {
	var out money.Amount
	if err := out.Scan(total.Format()); err != nil {
		panic(err)
	}
	return out
}
