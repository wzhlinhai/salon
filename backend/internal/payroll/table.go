package payroll

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/commission"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func monthDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func livePayroll(ctx context.Context, env ops.Env, month payrollmonth.Month) (*Payroll, error) {
	staffRows, err := env.Q.ListPayrollStaff(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	salaries, err := BaseSalariesInForce(ctx, env.Q, env.Caller.StoreID, month.First())
	if err != nil {
		return nil, err
	}
	commissions, err := commission.Commissions(ctx, env.Q, env.Caller.StoreID, month.First(), month.Last(), nil)
	if err != nil {
		return nil, err
	}
	adjustments, err := env.Q.PayrollMonthAdjustments(ctx, dbgen.PayrollMonthAdjustmentsParams{StoreID: env.Caller.StoreID, Month: monthDate(month.First())})
	if err != nil {
		return nil, err
	}
	byStaff := map[uuid.UUID]map[string]money.SignedTotal{}
	for _, c := range commissions {
		if byStaff[c.Staff.ID] == nil {
			byStaff[c.Staff.ID] = map[string]money.SignedTotal{}
		}
		byStaff[c.Staff.ID][c.Source] = byStaff[c.Staff.ID][c.Source].Add(c.Amount)
	}
	sums := map[uuid.UUID]money.SignedTotal{}
	for _, a := range adjustments {
		sums[a.StaffID] = sums[a.StaffID].Add(a.Amount)
	}
	times := make([]time.Time, 0, len(staffRows)*2)
	for _, row := range staffRows {
		times = append(times, row.CreatedAt.Time, row.DeactivatedAt.Time)
	}
	days, err := settings.BusinessDates(ctx, env.Q, env.Caller.StoreID, times)
	if err != nil {
		return nil, err
	}
	out := &Payroll{Month: month.String(), Lines: []PayrollLine{}}
	for i, row := range staffRows {
		salary, hasSalary := salaries[row.ID]
		employed := !month.Before(payrollmonth.Of(days[2*i])) && (row.Active || !payrollmonth.Of(days[2*i+1]).Before(month))
		_, hasCommission := byStaff[row.ID]
		_, hasAdjustment := sums[row.ID]
		if (!employed || salary.Amount.Fen() <= 0) && !hasCommission && !hasAdjustment {
			continue
		}
		line := PayrollLine{Staff: staff.Ref{ID: row.ID, Name: row.Name}, Active: row.Active, Mode: "base"}
		if hasSalary {
			line.Mode = salary.Mode
		}
		if employed {
			line.AppliedAmount = salary.Amount
		}
		if err := line.ItemCommission.Scan(byStaff[row.ID]["item"].Format()); err != nil {
			return nil, err
		}
		if err := line.ConsumeCommission.Scan(byStaff[row.ID]["consume"].Format()); err != nil {
			return nil, err
		}
		if err := line.LaborCommission.Scan(byStaff[row.ID]["labor"].Format()); err != nil {
			return nil, err
		}
		if err := line.SaleCommission.Scan(byStaff[row.ID]["sale"].Format()); err != nil {
			return nil, err
		}
		if err := line.AdjustmentsTotal.Scan(sums[row.ID].Format()); err != nil {
			return nil, err
		}
		total := money.SignedTotal{}.Add(line.ItemCommission).Add(line.ConsumeCommission).Add(line.LaborCommission).Add(line.SaleCommission)
		if err := line.CommissionTotal.Scan(total.Format()); err != nil {
			return nil, err
		}
		if line.Mode == "base" {
			line.BasePay = money.ChangeFromFen(line.AppliedAmount.Fen())
		} else {
			topUp := max(int64(0), line.AppliedAmount.Fen()-line.CommissionTotal.Fen())
			line.GuaranteeTopUp, err = money.FromFen(topUp)
			if err != nil {
				return nil, err
			}
		}
		total = total.Add(line.BasePay).Add(money.ChangeFromFen(line.GuaranteeTopUp.Fen())).Add(line.AdjustmentsTotal)
		if err := line.Total.Scan(total.Format()); err != nil {
			return nil, err
		}
		out.Lines = append(out.Lines, line)
	}
	if err := payrollTotals(out); err != nil {
		return nil, err
	}
	return out, nil
}

func payrollTotals(out *Payroll) error {
	sumAppliedAmount := money.SignedTotal{}
	sumBasePay := money.SignedTotal{}
	sumItemCommission := money.SignedTotal{}
	sumConsumeCommission := money.SignedTotal{}
	sumLaborCommission := money.SignedTotal{}
	sumSaleCommission := money.SignedTotal{}
	sumCommissionTotal := money.SignedTotal{}
	sumGuaranteeTopUp := money.SignedTotal{}
	sumAdjustmentsTotal := money.SignedTotal{}
	sumTotal := money.SignedTotal{}
	for _, line := range out.Lines {
		sumAppliedAmount = sumAppliedAmount.Add(money.ChangeFromFen(line.AppliedAmount.Fen()))
		sumBasePay = sumBasePay.Add(line.BasePay)
		sumItemCommission = sumItemCommission.Add(line.ItemCommission)
		sumConsumeCommission = sumConsumeCommission.Add(line.ConsumeCommission)
		sumLaborCommission = sumLaborCommission.Add(line.LaborCommission)
		sumSaleCommission = sumSaleCommission.Add(line.SaleCommission)
		sumCommissionTotal = sumCommissionTotal.Add(line.CommissionTotal)
		sumGuaranteeTopUp = sumGuaranteeTopUp.Add(money.ChangeFromFen(line.GuaranteeTopUp.Fen()))
		sumAdjustmentsTotal = sumAdjustmentsTotal.Add(line.AdjustmentsTotal)
		sumTotal = sumTotal.Add(line.Total)
	}
	if err := out.Totals.AppliedAmount.Scan(sumAppliedAmount.Format()); err != nil {
		return err
	}
	if err := out.Totals.BasePay.Scan(sumBasePay.Format()); err != nil {
		return err
	}
	if err := out.Totals.ItemCommission.Scan(sumItemCommission.Format()); err != nil {
		return err
	}
	if err := out.Totals.ConsumeCommission.Scan(sumConsumeCommission.Format()); err != nil {
		return err
	}
	if err := out.Totals.LaborCommission.Scan(sumLaborCommission.Format()); err != nil {
		return err
	}
	if err := out.Totals.SaleCommission.Scan(sumSaleCommission.Format()); err != nil {
		return err
	}
	if err := out.Totals.CommissionTotal.Scan(sumCommissionTotal.Format()); err != nil {
		return err
	}
	if err := out.Totals.GuaranteeTopUp.Scan(sumGuaranteeTopUp.Format()); err != nil {
		return err
	}
	if err := out.Totals.AdjustmentsTotal.Scan(sumAdjustmentsTotal.Format()); err != nil {
		return err
	}
	if err := out.Totals.Total.Scan(sumTotal.Format()); err != nil {
		return err
	}
	return nil
}

func lockedPayroll(ctx context.Context, env ops.Env, settlement *payrollmonth.Settlement) (*Payroll, error) {
	rows, err := env.Q.PayrollLines(ctx, dbgen.PayrollLinesParams{StoreID: env.Caller.StoreID, SettlementID: settlement.ID})
	if err != nil {
		return nil, err
	}
	people, err := env.Q.ListPayrollStaff(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	refs := map[uuid.UUID]staff.Ref{}
	active := map[uuid.UUID]bool{}
	for _, person := range people {
		refs[person.ID] = staff.Ref{ID: person.ID, Name: person.Name}
		active[person.ID] = person.Active
	}
	by := refs[settlement.SettledBy]
	out := &Payroll{Month: settlement.Month.String(), Locked: true, LockedBy: &by, LockedAt: &settlement.SettledAt, Lines: []PayrollLine{}}
	for _, row := range rows {
		out.Lines = append(out.Lines, PayrollLine{Staff: refs[row.StaffID], Active: active[row.StaffID], Mode: row.Mode, PayrollTotals: PayrollTotals{
			AppliedAmount: row.AppliedAmount, BasePay: row.BasePay, ItemCommission: row.ItemCommission, ConsumeCommission: row.ConsumeCommission, LaborCommission: row.LaborCommission, SaleCommission: row.SaleCommission, CommissionTotal: row.CommissionTotal, GuaranteeTopUp: row.GuaranteeTopUp, AdjustmentsTotal: row.AdjustmentsTotal, Total: row.Total}})
	}
	if err := payrollTotals(out); err != nil {
		return nil, err
	}
	return out, nil
}

func getPayroll(ctx context.Context, env ops.Env, in *MonthInput) (*Payroll, error) {
	month, err := payrollmonth.Check(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	settlement, err := payrollmonth.GetSettlement(ctx, env.Q, env.Caller.StoreID, month)
	if err != nil {
		return nil, err
	}
	if settlement != nil {
		return lockedPayroll(ctx, env, settlement)
	}
	return livePayroll(ctx, env, month)
}

func listPayrollMonths(ctx context.Context, env ops.Env, _ *ListPayrollMonthsInput) (*PayrollMonthList, error) {
	rows, err := payrollmonth.List(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	for _, row := range rows {
		if row.Settlement != nil {
			ids = append(ids, row.Settlement.SettledBy)
		}
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	out := &PayrollMonthList{Entries: []PayrollMonth{}}
	for _, row := range rows {
		entry := PayrollMonth{Month: row.Month.String(), Locked: row.Settlement != nil, Lockable: row.Lockable}
		if row.Settlement != nil {
			by := refs[row.Settlement.SettledBy]
			entry.LockedBy = &by
			entry.LockedAt = &row.Settlement.SettledAt
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func lockPayrollMonth(ctx context.Context, env ops.Env, in *MonthInput) (*Payroll, error) {
	settlement, err := payrollmonth.Lock(ctx, env.Q, env.Caller.StoreID, in.Month, env.Caller.StaffID, env.Now)
	if err != nil {
		return nil, err
	}
	if err := commission.Settle(ctx, env.Q, env.Caller.StoreID, *settlement); err != nil {
		return nil, err
	}
	// 先存提成，工资表再读刚存下的提成，工资表各列与提成明细出自同一份数据。
	live, err := livePayroll(ctx, env, settlement.Month)
	if err != nil {
		return nil, err
	}
	for _, line := range live.Lines {
		if err := env.Q.AddPayrollLine(ctx, dbgen.AddPayrollLineParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, SettlementID: settlement.ID, StaffID: line.Staff.ID, Mode: line.Mode,
			AppliedAmount: line.AppliedAmount, BasePay: line.BasePay, ItemCommission: line.ItemCommission, ConsumeCommission: line.ConsumeCommission, LaborCommission: line.LaborCommission, SaleCommission: line.SaleCommission, CommissionTotal: line.CommissionTotal, GuaranteeTopUp: line.GuaranteeTopUp, AdjustmentsTotal: line.AdjustmentsTotal, Total: line.Total}); err != nil {
			return nil, err
		}
	}
	out, err := lockedPayroll(ctx, env, settlement)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "lock_payroll_month", audit.PayrollMonthTarget(settlement.ID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}
