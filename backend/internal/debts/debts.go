package debts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Register 声明欠款查询、还款、历史欠款登记和对账操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListCustomerDebtsInput, CustomerDebtList]{Name: "list_customer_debts", Summary: "查看顾客逐笔欠款和还款", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCustomerDebts})
	ops.Register(r, ops.Operation[RepayDebtInput, Debt]{Name: "repay_debt", Summary: "偿还一笔欠款的全部或部分", Kind: ops.Write, Permission: perm.PaymentCollect, AI: ops.AIDraft, Handle: repayDebt})
	ops.Register(r, ops.Operation[RegisterHistoricalDebtInput, Debt]{Name: "register_historical_debt", Summary: "登记不关联单的历史欠款", Kind: ops.Write, Permission: perm.DataImportExport, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, in *RegisterHistoricalDebtInput) (*Debt, error) {
		return RegisterHistoricalDebt(ctx, env, in)
	}})
	ops.Register(r, ops.Operation[DebtSummaryInput, Summary]{Name: "debt_summary", Summary: "本店挂账、历史欠款、已还及未还对账", Kind: ops.Read, Permission: perm.ReportView, AI: ops.AIDirect, Handle: debtSummary})
}

// RecordOrderDebt 在调用者事务中为单上的一笔挂账登记欠款。
func RecordOrderDebt(ctx context.Context, env ops.Env, customerID, orderID, paymentID uuid.UUID, amount money.Amount, day time.Time) error {
	_, err := env.Q.CreateDebt(ctx, dbgen.CreateDebtParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: customerID, OrderID: &orderID, OrderPaymentID: &paymentID, Amount: amount, BusinessDate: pgtype.Date{Time: day, Valid: true}, RecordedBy: env.Caller.StaffID})
	return err
}

// RegisterHistoricalDebt 在调用者事务中校验并登记历史欠款，供操作和导入复用。
func RegisterHistoricalDebt(ctx context.Context, env ops.Env, in *RegisterHistoricalDebtInput) (*Debt, error) {
	return registerHistoricalDebtAt(ctx, env, in, time.Now())
}

func registerHistoricalDebtAt(ctx context.Context, env ops.Env, in *RegisterHistoricalDebtInput, now time.Time) (*Debt, error) {
	if in.Amount.Cmp(money.Amount{}) <= 0 {
		return nil, ops.Rule("欠款金额须大于零")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	incurred, err := time.Parse(time.DateOnly, in.IncurredOn)
	if err != nil {
		return nil, ops.Rule("欠款日期无效")
	}
	if incurred.Format(time.DateOnly) > day.Format(time.DateOnly) {
		return nil, ops.Rule("欠款日期不能晚于今天")
	}
	if _, err := customers.LockReference(ctx, env, in.CustomerID); err != nil {
		return nil, rowError(err)
	}
	row, err := env.Q.CreateDebt(ctx, dbgen.CreateDebtParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: in.CustomerID, Amount: in.Amount, BusinessDate: pgtype.Date{Time: incurred, Valid: true}, Note: in.Note, RecordedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	out, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "register_historical_debt", audit.CustomerTarget(row.CustomerID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}

func listCustomerDebts(ctx context.Context, env ops.Env, in *ListCustomerDebtsInput) (*CustomerDebtList, error) {
	if err := customers.CheckViewable(ctx, env, in.CustomerID); err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCustomerDebts(ctx, dbgen.ListCustomerDebtsParams{StoreID: env.Caller.StoreID, CustomerID: in.CustomerID})
	if err != nil {
		return nil, err
	}
	items, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	out := &CustomerDebtList{Items: []Debt{}}
	var amount, repaid, outstanding money.Total
	for _, d := range items {
		if in.OutstandingOnly && d.Outstanding.Cmp(money.Amount{}) == 0 {
			continue
		}
		out.Items = append(out.Items, d)
		amount = amount.AddProduct(d.Amount, 1)
		repaid = repaid.AddProduct(d.Repaid, 1)
		outstanding = outstanding.AddProduct(d.Outstanding, 1)
	}
	out.Totals = Totals{Amount: amount.Format(), Repaid: repaid.Format(), Outstanding: outstanding.Format()}
	return out, nil
}

func views(ctx context.Context, env ops.Env, rows []dbgen.Debt) ([]Debt, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	repayments, err := env.Q.ListDebtRepayments(ctx, dbgen.ListDebtRepaymentsParams{StoreID: env.Caller.StoreID, DebtIds: ids})
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID][]Repayment{}
	for _, r := range repayments {
		byID[r.DebtID] = append(byID[r.DebtID], Repayment{ID: r.ID, Amount: r.Amount, Method: r.Method, BusinessDate: r.BusinessDate.Time.Format(time.DateOnly), RepaidBy: r.RepaidBy, At: r.At.Time})
	}
	out := make([]Debt, 0, len(rows))
	for _, r := range rows {
		ps := byID[r.ID]
		if ps == nil {
			ps = []Repayment{}
		}
		repaid := money.Amount{}
		for _, p := range ps {
			repaid, err = repaid.Add(p.Amount)
			if err != nil {
				return nil, err
			}
		}
		outstanding, err := r.Amount.Apply(money.Amount{}.Sub(repaid))
		if err != nil {
			return nil, err
		}
		kind := "historical"
		if r.OrderID != nil {
			kind = "order"
		}
		out = append(out, Debt{ID: r.ID, Kind: kind, OrderID: r.OrderID, BusinessDate: r.BusinessDate.Time.Format(time.DateOnly), Amount: r.Amount, Repaid: repaid, Outstanding: outstanding, Note: r.Note, RecordedBy: r.RecordedBy, At: r.At.Time, Repayments: ps})
	}
	return out, nil
}

func view(ctx context.Context, env ops.Env, row dbgen.Debt) (*Debt, error) {
	ds, err := views(ctx, env, []dbgen.Debt{row})
	if err != nil {
		return nil, err
	}
	return &ds[0], nil
}

func repayDebt(ctx context.Context, env ops.Env, in *RepayDebtInput) (*Debt, error) {
	return repayDebtAt(ctx, env, in, time.Now())
}

func repayDebtAt(ctx context.Context, env ops.Env, in *RepayDebtInput, now time.Time) (*Debt, error) {
	if in.Amount.Cmp(money.Amount{}) <= 0 {
		return nil, ops.Rule("还款金额须大于零")
	}
	row, err := env.Q.LockDebt(ctx, dbgen.LockDebtParams{StoreID: env.Caller.StoreID, ID: in.DebtID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := customers.CheckViewable(ctx, env, row.CustomerID); err != nil {
		return nil, err
	}
	before, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if before.Outstanding.Cmp(money.Amount{}) == 0 {
		return nil, ops.Rule("这笔欠款已还清")
	}
	if in.Amount.Cmp(before.Outstanding) > 0 {
		return nil, ops.Rule("还款超过未还金额")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	if err := env.Q.CreateDebtRepayment(ctx, dbgen.CreateDebtRepaymentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, DebtID: row.ID, Amount: in.Amount, Method: in.Method, BusinessDate: pgtype.Date{Time: day, Valid: true}, RepaidBy: env.Caller.StaffID}); err != nil {
		return nil, err
	}
	out, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "repay_debt", audit.CustomerTarget(row.CustomerID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func debtSummary(ctx context.Context, env ops.Env, _ *DebtSummaryInput) (*Summary, error) {
	rows, err := env.Q.DebtSummary(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	zero := Totals{Amount: "0.00", Repaid: "0.00", Outstanding: "0.00"}
	out := &Summary{Totals: zero, Order: zero, Historical: zero}
	var amount, repaid, outstanding money.Total
	for _, r := range rows {
		var a, p, o money.Total
		if err := a.Scan(r.Amount); err != nil {
			return nil, err
		}
		if err := p.Scan(r.Repaid); err != nil {
			return nil, err
		}
		if err := o.Scan(r.Outstanding); err != nil {
			return nil, err
		}
		totals := Totals{Amount: a.Format(), Repaid: p.Format(), Outstanding: o.Format()}
		if r.IsOrder {
			out.Order = totals
		} else {
			out.Historical = totals
		}
		// SQL 已按来源汇总，累计金额仍保持十进制且不限单笔上限。
		amount = amount.AddTotal(a)
		repaid = repaid.AddTotal(p)
		outstanding = outstanding.AddTotal(o)
		out.OutstandingCount += r.OutstandingCount
	}
	out.Totals = Totals{Amount: amount.Format(), Repaid: repaid.Format(), Outstanding: outstanding.Format()}
	return out, nil
}
