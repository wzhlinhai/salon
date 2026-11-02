package payroll

import (
	"context"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/commission"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// ListPayrollCommissionsInput 是员工结算月提成明细的筛选和分页参数。
type ListPayrollCommissionsInput struct {
	MonthInput
	StaffID  uuid.UUID  `json:"staff_id"`
	Source   *string    `json:"source,omitempty" enum:"item,consume,labor,sale"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
}

func listPayrollCommissions(ctx context.Context, env ops.Env, in *ListPayrollCommissionsInput) (*commission.CommissionList, error) {
	month, err := CheckMonth(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	rows, err := commission.Commissions(ctx, env.Q, env.Caller.StoreID, month, month.AddDate(0, 1, -1), &in.StaffID)
	if err != nil {
		return nil, err
	}
	return commission.Page(rows, in.Source, in.Limit, in.BeforeID), nil
}
