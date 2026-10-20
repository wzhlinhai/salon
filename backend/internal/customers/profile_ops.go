package customers

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// CreateCustomerInput 是顾客建档资料，全空表示未识别顾客。
type CreateCustomerInput struct {
	Name     *string    `json:"name,omitempty"`
	Phone    *string    `json:"phone,omitempty"`
	Gender   *string    `json:"gender,omitempty" enum:"male,female"`
	SourceID *uuid.UUID `json:"source_id,omitempty"`
	Note     *string    `json:"note,omitempty"`
}

// CreateCustomerResult 返回建档或按号码找到的顾客。
type CreateCustomerResult struct {
	Customer Customer `json:"customer"`
	Existed  bool     `json:"existed"`
}

// UpdateCustomerProfileInput 是顾客资料的完整替换参数。
type UpdateCustomerProfileInput struct {
	ID       uuid.UUID  `json:"id"`
	Name     *string    `json:"name,omitempty"`
	Gender   *string    `json:"gender,omitempty" enum:"male,female"`
	SourceID *uuid.UUID `json:"source_id,omitempty"`
	Note     *string    `json:"note,omitempty"`
}

// CustomerPhoneInput 是补充或修改顾客手机号的参数。
type CustomerPhoneInput struct {
	ID    uuid.UUID `json:"id"`
	Phone string    `json:"phone"`
}

func registerProfiles(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateCustomerInput, CreateCustomerResult]{
		Name: "create_customer", Summary: "按手机号查找或新建顾客，无手机号则建未识别顾客", Kind: ops.Write,
		Permission: perm.CustomerEdit, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreateCustomerInput) (*CreateCustomerResult, error) {
			phone, err := normalizePhone(in.Phone)
			if err != nil {
				return nil, err
			}
			if phone != nil {
				if err := env.Q.LockCustomerPhone(ctx, *phone); err != nil {
					return nil, err
				}
				row, err := env.Q.FindCustomerByPhone(ctx, phone)
				if err == nil {
					if !canView(env.Caller, row) {
						return nil, ops.Rule("这个手机号已有档案，但不在你能查看的范围内")
					}
					c, err := view(ctx, env, row)
					if err != nil {
						return nil, err
					}
					return &CreateCustomerResult{Customer: *mask(env, c), Existed: true}, nil
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return nil, err
				}
			}
			if err := checkSource(ctx, env, in.SourceID, nil); err != nil {
				return nil, err
			}
			row, err := env.Q.CreateCustomer(ctx, dbgen.CreateCustomerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name, Phone: phone, Gender: in.Gender, SourceID: in.SourceID, Note: in.Note, CreatedBy: env.Caller.StaffID})
			if err != nil {
				return nil, rowError(err)
			}
			c, err := view(ctx, env, row)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "create_customer", row.ID, nil, c); err != nil {
				return nil, err
			}
			return &CreateCustomerResult{Customer: *mask(env, c)}, nil
		},
	})
	ops.Register(r, ops.Operation[UpdateCustomerProfileInput, Customer]{
		Name: "update_customer_profile", Summary: "修改顾客姓名、性别、来源与备注", Kind: ops.Write,
		Permission: perm.CustomerEdit, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateCustomerProfileInput) (*Customer, error) {
			row, err := lock(ctx, env, in.ID)
			if err != nil {
				return nil, err
			}
			before, err := view(ctx, env, row)
			if err != nil {
				return nil, err
			}
			if err := checkSource(ctx, env, in.SourceID, row.SourceID); err != nil {
				return nil, err
			}
			row.Name = in.Name
			row.Gender = in.Gender
			row.SourceID = in.SourceID
			row.Note = in.Note
			return finish(ctx, env, "update_customer_profile", before, row)
		},
	})

	ops.Register(r, ops.Operation[CustomerPhoneInput, Customer]{
		Name: "add_customer_phone", Summary: "给还没有手机号的顾客补上手机号", Kind: ops.Write,
		Permission: perm.CustomerEdit, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CustomerPhoneInput) (*Customer, error) {
			return updatePhone(ctx, env, in, true)
		},
	})
	ops.Register(r, ops.Operation[CustomerPhoneInput, Customer]{
		Name: "change_customer_phone", Summary: "修改顾客已有的手机号", Kind: ops.Write,
		Permission: perm.CustomerMerge, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CustomerPhoneInput) (*Customer, error) {
			return updatePhone(ctx, env, in, false)
		},
	})
}

// updatePhone 在锁定并检查查看范围后，补充或修改顾客手机号。
func updatePhone(ctx context.Context, env ops.Env, in *CustomerPhoneInput, add bool) (*Customer, error) {
	row, err := lock(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	before, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if add && row.Phone != nil {
		return nil, ops.Rule("该顾客已有手机号，修改请找有权限的人")
	}
	if !add && row.Phone == nil {
		return nil, ops.Rule("该顾客还没有手机号，请先补充手机号")
	}
	phone, err := normalizePhone(&in.Phone)
	if err != nil {
		return nil, err
	}
	if err := env.Q.LockCustomerPhone(ctx, *phone); err != nil {
		return nil, err
	}
	found, err := env.Q.FindCustomerByPhone(ctx, phone)
	if err == nil && found.ID != row.ID {
		return nil, ops.Rule("这个手机号已属于另一位顾客，可以合并这两位顾客")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	row.Phone = phone
	action := "change_customer_phone"
	if add {
		action = "add_customer_phone"
	}
	return finish(ctx, env, action, before, row)
}
