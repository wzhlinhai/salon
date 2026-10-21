package customers

import (
	"bytes"
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// AssignCustomerResponsibleInput 是批量分配负责人或清空负责人的参数。
type AssignCustomerResponsibleInput struct {
	CustomerIDs []uuid.UUID `json:"customer_ids" minItems:"1" maxItems:"200" uniqueItems:"true"`
	StaffID     *uuid.UUID  `json:"staff_id,omitempty"`
}

// HandOverCustomersInput 是员工离职等情况下的全部顾客交接参数。
type HandOverCustomersInput struct {
	FromStaffID uuid.UUID `json:"from_staff_id"`
	ToStaffID   uuid.UUID `json:"to_staff_id"`
}

// CustomerAssignmentResult 是分配后的顾客列表。
type CustomerAssignmentResult struct {
	Items []Customer `json:"items"`
}

// CustomerHandoverResult 是交接人数及完整顾客清单。
type CustomerHandoverResult struct {
	Count       int         `json:"count"`
	ToStaffID   uuid.UUID   `json:"to_staff_id"`
	CustomerIDs []uuid.UUID `json:"customer_ids"`
}

func registerAssignments(r *ops.Registry) {
	ops.Register(r, ops.Operation[AssignCustomerResponsibleInput, CustomerAssignmentResult]{
		Name: "assign_customer_responsible", Summary: "批量分配顾客负责人", Kind: ops.Write,
		Permission: perm.CustomerAssign, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *AssignCustomerResponsibleInput) (*CustomerAssignmentResult, error) {
			if in.StaffID != nil {
				if err := activeStaff(ctx, env, *in.StaffID); err != nil {
					return nil, err
				}
			}
			ids := slices.Clone(in.CustomerIDs)
			slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
			items, err := change(ctx, env, "assign_customer_responsible", byIDs(ids...), func(rows []dbgen.Customer) error {
				for i := range rows {
					rows[i].ResponsibleStaffID = in.StaffID
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			out := &CustomerAssignmentResult{Items: []Customer{}}
			for _, c := range items {
				out.Items = append(out.Items, *c)
			}
			return out, nil
		},
	})
	ops.Register(r, ops.Operation[HandOverCustomersInput, CustomerHandoverResult]{
		Name: "hand_over_customers", Summary: "将员工负责的全部未合并顾客移交给在职员工", Kind: ops.Write,
		Permission: perm.CustomerAssign, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *HandOverCustomersInput) (*CustomerHandoverResult, error) {
			if in.FromStaffID == in.ToStaffID {
				return nil, ops.Rule("交接的两名员工不能相同")
			}
			ids := []uuid.UUID{in.FromStaffID, in.ToStaffID}
			slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
			for _, id := range ids {
				st, err := env.Q.ShareCustomerStaff(ctx, id)
				if err != nil {
					return nil, rowError(err)
				}
				if id == in.ToStaffID && !st.Active {
					return nil, ops.Rule("负责人必须是在职员工")
				}
			}
			items, err := change(ctx, env, "hand_over_customers", responsibleOf(in.FromStaffID), func(rows []dbgen.Customer) error {
				for i := range rows {
					rows[i].ResponsibleStaffID = &in.ToStaffID
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			out := &CustomerHandoverResult{Count: len(items), ToStaffID: in.ToStaffID, CustomerIDs: []uuid.UUID{}}
			for _, c := range items {
				out.CustomerIDs = append(out.CustomerIDs, c.ID)
			}
			before := struct {
				CustomerCount int `json:"customer_count"`
			}{CustomerCount: len(items)}
			after := struct {
				CustomerCount int       `json:"customer_count"`
				ToStaffID     uuid.UUID `json:"to_staff_id"`
			}{ToStaffID: in.ToStaffID}
			if err := audit.Record(ctx, env, "hand_over_customers", audit.StaffTarget(in.FromStaffID), before, after); err != nil {
				return nil, err
			}
			return out, nil
		},
	})
}
