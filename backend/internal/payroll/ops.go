package payroll

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明底薪和加减项的五个工资管理操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[SetBaseSalaryInput, BaseSalary]{Name: "set_base_salary", Summary: "追加员工底薪设置", Kind: ops.Write, Permission: perm.PayrollManage, AI: ops.AIDraft, Handle: setBaseSalary})
	ops.Register(r, ops.Operation[MonthInput, BaseSalaryList]{Name: "list_base_salaries", Summary: "查看结算月每位员工执行的底薪", Kind: ops.Read, Permission: perm.PayrollManage, AI: ops.AIDirect, Handle: listBaseSalaries})
	ops.Register(r, ops.Operation[AddPayrollAdjustmentInput, PayrollAdjustment]{Name: "add_payroll_adjustment", Summary: "添加员工月度加减项", Kind: ops.Write, Permission: perm.PayrollManage, AI: ops.AIDraft, Handle: addPayrollAdjustment})
	ops.Register(r, ops.Operation[DeletePayrollAdjustmentInput, staff.OK]{Name: "delete_payroll_adjustment", Summary: "删除员工加减项", Kind: ops.Write, Permission: perm.PayrollManage, AI: ops.AIDraft, Handle: deletePayrollAdjustment})
	ops.Register(r, ops.Operation[ListPayrollAdjustmentsInput, PayrollAdjustmentList]{Name: "list_payroll_adjustments", Summary: "列出结算月加减项", Kind: ops.Read, Permission: perm.PayrollManage, AI: ops.AIDirect, Handle: listPayrollAdjustments})
}

func setBaseSalary(ctx context.Context, env ops.Env, in *SetBaseSalaryInput) (*BaseSalary, error) {
	if err := env.Q.LockPayroll(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	month, err := CheckMonth(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	if _, err := staff.LockReferences(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		return nil, err
	}
	salaries, err := BaseSalariesInForce(ctx, env.Q, env.Caller.StoreID, month)
	if err != nil {
		return nil, err
	}
	var before any
	if old, ok := salaries[in.StaffID]; ok {
		before = old
	}
	row, err := env.Q.AddBaseSalary(ctx, dbgen.AddBaseSalaryParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: in.StaffID, EffectiveMonth: pgtype.Date{Time: month, Valid: true}, Mode: in.Mode, Amount: in.Amount, SavedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, []uuid.UUID{row.StaffID, row.SavedBy})
	if err != nil {
		return nil, err
	}
	after := salaryView(row, refs)
	if err := audit.Record(ctx, env, "set_base_salary", audit.BaseSalaryTarget(in.StaffID), before, after); err != nil {
		return nil, err
	}
	return &after, nil
}

func addPayrollAdjustment(ctx context.Context, env ops.Env, in *AddPayrollAdjustmentInput) (*PayrollAdjustment, error) {
	if err := env.Q.LockPayroll(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	month, err := CheckMonth(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ops.Rule("加减项名称不能为空")
	}
	if in.Amount.Fen() == 0 {
		return nil, ops.Rule("加减项金额不能为 0")
	}
	if _, err := staff.LockReferences(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		return nil, err
	}
	row, err := env.Q.AddPayrollAdjustment(ctx, dbgen.AddPayrollAdjustmentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Month: pgtype.Date{Time: month, Valid: true}, StaffID: in.StaffID, Name: name, Amount: in.Amount, Note: in.Note, CreatedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	views, err := adjustmentViews(ctx, env, []dbgen.PayrollAdjustment{row})
	if err != nil {
		return nil, err
	}
	after := views[0]
	if err := audit.Record(ctx, env, "add_payroll_adjustment", audit.PayrollAdjustmentTarget(row.ID), nil, after); err != nil {
		return nil, err
	}
	return &after, nil
}

func deletePayrollAdjustment(ctx context.Context, env ops.Env, in *DeletePayrollAdjustmentInput) (*staff.OK, error) {
	if err := env.Q.LockPayroll(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	row, err := env.Q.GetPayrollAdjustment(ctx, dbgen.GetPayrollAdjustmentParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("加减项不存在：%w", ops.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	views, err := adjustmentViews(ctx, env, []dbgen.PayrollAdjustment{row})
	if err != nil {
		return nil, err
	}
	if err := env.Q.DeletePayrollAdjustment(ctx, dbgen.DeletePayrollAdjustmentParams{StoreID: env.Caller.StoreID, ID: in.ID}); err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "delete_payroll_adjustment", audit.PayrollAdjustmentTarget(row.ID), views[0], nil); err != nil {
		return nil, err
	}
	return &staff.OK{OK: true}, nil
}
