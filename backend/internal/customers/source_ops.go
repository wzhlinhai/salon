package customers

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// CreateCustomerSourceInput 是新建来源的参数。
type CreateCustomerSourceInput struct {
	Name string `json:"name" minLength:"1" maxLength:"50"`
}

// UpdateCustomerSourceInput 是修改或停用来源的参数。
type UpdateCustomerSourceInput struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name" minLength:"1" maxLength:"50"`
	Active bool      `json:"active"`
}

func registerSources(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateCustomerSourceInput, CustomerSource]{
		Name: "create_customer_source", Summary: "新建顾客来源", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreateCustomerSourceInput) (*CustomerSource, error) {
			row, err := env.Q.CreateCustomerSource(ctx, dbgen.CreateCustomerSourceParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name})
			if err != nil {
				return nil, rowError(err)
			}
			out := sourceView(row)
			if err := audit.Record(ctx, env, "create_customer_source", audit.CustomerSourceTarget(row.ID), nil, out); err != nil {
				return nil, err
			}
			return &out, nil
		},
	})
	ops.Register(r, ops.Operation[UpdateCustomerSourceInput, CustomerSource]{
		Name: "update_customer_source", Summary: "修改或停用顾客来源", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateCustomerSourceInput) (*CustomerSource, error) {
			row, err := env.Q.LockCustomerSource(ctx, in.ID)
			if err != nil {
				return nil, rowError(err)
			}
			if row.StoreID != env.Caller.StoreID {
				return nil, ops.ErrNotFound
			}
			before := sourceView(row)
			row, err = env.Q.UpdateCustomerSource(ctx, dbgen.UpdateCustomerSourceParams{ID: in.ID, Name: in.Name, Active: in.Active})
			if err != nil {
				return nil, rowError(err)
			}
			out := sourceView(row)
			if err := audit.Record(ctx, env, "update_customer_source", audit.CustomerSourceTarget(row.ID), before, out); err != nil {
				return nil, err
			}
			return &out, nil
		},
	})
}
