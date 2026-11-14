package customers

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
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
			row, existed, err := findOrCreate(ctx, env, customerLookup{Name: in.Name, Phone: in.Phone, Gender: in.Gender, SourceID: in.SourceID, Note: in.Note})
			if err != nil {
				return nil, err
			}
			c, err := view(ctx, env, row)
			if err != nil {
				return nil, err
			}
			return &CreateCustomerResult{Customer: *mask(env, c), Existed: existed}, nil
		},
	})
	ops.Register(r, ops.Operation[UpdateCustomerProfileInput, Customer]{
		Name: "update_customer_profile", Summary: "修改顾客姓名、性别、来源与备注", Kind: ops.Write,
		Permission: perm.CustomerEdit, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateCustomerProfileInput) (*Customer, error) {
			return changeOne(ctx, env, "update_customer_profile", in.ID, func(row *dbgen.Customer) error {
				if err := checkSource(ctx, env, in.SourceID, row.SourceID); err != nil {
					return err
				}
				row.Name = in.Name
				row.Gender = in.Gender
				row.SourceID = in.SourceID
				row.Note = in.Note
				return nil
			})
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
	action := "change_customer_phone"
	if add {
		action = "add_customer_phone"
	}
	return changeOne(ctx, env, action, in.ID, func(row *dbgen.Customer) error {
		if add && row.Phone != nil {
			return ops.Rule("该顾客已有手机号，修改请找有权限的人")
		}
		if !add && row.Phone == nil {
			return ops.Rule("该顾客还没有手机号，请先补充手机号")
		}
		phone, err := normalizePhone(&in.Phone)
		if err != nil {
			return err
		}
		if err := env.Q.LockCustomerPhone(ctx, *phone); err != nil {
			return err
		}
		found, err := env.Q.FindCustomerByPhone(ctx, phone)
		if err == nil && found.ID != row.ID {
			return ops.Rule("这个手机号已属于另一位顾客，可以合并这两位顾客")
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row.Phone = phone
		return nil
	})
}

// CreateUnidentified 为开单创建未识别顾客并记新建日志；由开单权限覆盖，无需 customer.edit 权限。
func CreateUnidentified(ctx context.Context, env ops.Env, gender string) (uuid.UUID, error) {
	row, err := env.Q.CreateCustomer(ctx, dbgen.CreateCustomerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Gender: &gender, CreatedBy: &env.Caller.StaffID})
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := created(ctx, env, "create_customer", row); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

// customerLookup 是按手机号查找或新建顾客所需的档案字段。
type customerLookup struct {
	Name     *string
	Phone    *string
	Gender   *string
	SourceID *uuid.UUID
	Note     *string
}

// findOrCreate 让顾客建档与开单/预约共用同一套手机号查重、查看范围检查和新建日志。
func findOrCreate(ctx context.Context, env ops.Env, in customerLookup) (dbgen.Customer, bool, error) {
	phone, err := normalizePhone(in.Phone)
	if err != nil {
		return dbgen.Customer{}, false, err
	}
	if phone != nil {
		if err := env.Q.LockCustomerPhone(ctx, *phone); err != nil {
			return dbgen.Customer{}, false, err
		}
		row, err := env.Q.FindCustomerByPhone(ctx, phone)
		if err == nil {
			if !canView(env.Caller, row) {
				return dbgen.Customer{}, false, ops.Rule("这个手机号已有档案，但不在你能查看的范围内")
			}
			return row, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return dbgen.Customer{}, false, err
		}
	}
	if err := checkSource(ctx, env, in.SourceID, nil); err != nil {
		return dbgen.Customer{}, false, err
	}
	row, err := env.Q.CreateCustomer(ctx, dbgen.CreateCustomerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name, Phone: phone, Gender: in.Gender, SourceID: in.SourceID, Note: in.Note, CreatedBy: &env.Caller.StaffID})
	if err != nil {
		return dbgen.Customer{}, false, rowError(err)
	}
	if _, err := created(ctx, env, "create_customer", row); err != nil {
		return dbgen.Customer{}, false, err
	}
	return row, false, nil
}

// FindOrCreate 按手机号查找顾客，找不到或没有手机号时新建；新建时写建档日志。
// 供开单、预约等需要顾客但不必具备顾客建档权限的业务复用同一套查重与查看范围检查。
func FindOrCreate(ctx context.Context, env ops.Env, name, phone, gender *string) (dbgen.Customer, bool, error) {
	return findOrCreate(ctx, env, customerLookup{Name: name, Phone: phone, Gender: gender})
}

// FindOrCreateForCustomerLogin 按手机号查找或新建顾客，供小程序登录自动关联档案；新建顾客以顾客本人为操作者。
func FindOrCreateForCustomerLogin(ctx context.Context, env ops.Env, rawPhone string) (dbgen.Customer, bool, error) {
	phone, err := normalizePhone(&rawPhone)
	if err != nil {
		return dbgen.Customer{}, false, err
	}
	if err := env.Q.LockCustomerPhone(ctx, *phone); err != nil {
		return dbgen.Customer{}, false, err
	}
	row, err := env.Q.FindStoreCustomerByPhone(ctx, dbgen.FindStoreCustomerByPhoneParams{StoreID: env.Caller.StoreID, Phone: phone})
	if err == nil {
		row, err = SurvivingCustomer(ctx, env.Q, row.ID)
		if err == nil && row.StoreID != env.Caller.StoreID {
			return dbgen.Customer{}, false, ops.Rule("该顾客已合并到其他门店的档案，不能登录")
		}
		return row, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Customer{}, false, err
	}
	id := uuid.New()
	row, err = env.Q.CreateCustomerAsCustomer(ctx, dbgen.CreateCustomerAsCustomerParams{
		ID: id, TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Phone: phone, CreatedByCustomer: &id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// 同手机号已被并发的另一次登录创建，重新按本店手机号读取。
		row, err = env.Q.FindStoreCustomerByPhone(ctx, dbgen.FindStoreCustomerByPhoneParams{StoreID: env.Caller.StoreID, Phone: phone})
		if err == nil {
			row, err = SurvivingCustomer(ctx, env.Q, row.ID)
			return row, true, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if other, findErr := env.Q.FindCustomerByPhone(ctx, phone); findErr == nil && other.StoreID != env.Caller.StoreID {
				return dbgen.Customer{}, false, ops.Rule("这个手机号已属于其他门店的顾客")
			}
		}
	}
	if err != nil {
		return dbgen.Customer{}, false, rowError(err)
	}
	env.Caller.CustomerID = &id
	if _, err := created(ctx, env, "create_customer", row); err != nil {
		return dbgen.Customer{}, false, err
	}
	return row, false, nil
}

// SurvivingCustomer 沿 merged_into 找到合并后保留的顾客。
func SurvivingCustomer(ctx context.Context, q *dbgen.Queries, id uuid.UUID) (dbgen.Customer, error) {
	for range 100 {
		row, err := q.GetCustomer(ctx, id)
		if err != nil {
			return dbgen.Customer{}, err
		}
		if row.MergedInto == nil {
			return row, nil
		}
		if *row.MergedInto == id {
			return dbgen.Customer{}, errors.New("customer merged into itself")
		}
		id = *row.MergedInto
	}
	return dbgen.Customer{}, errors.New("customer merge chain too long")
}

// MaskPhone 返回顾客本人看到的打码手机号。
func MaskPhone(phone *string) *string {
	if phone == nil {
		return nil
	}
	masked := privacy.MaskPhone(*phone)
	return &masked
}
