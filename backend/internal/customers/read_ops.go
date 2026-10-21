package customers

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// CustomerIDInput 指定一位顾客。
type CustomerIDInput struct {
	ID uuid.UUID `json:"id"`
}

// SearchCustomersInput 是顾客搜索条件。
type SearchCustomersInput struct {
	Query               string     `json:"query,omitempty"`
	IncludeUnidentified bool       `json:"include_unidentified,omitempty"`
	ResponsibleStaffID  *uuid.UUID `json:"responsible_staff_id,omitempty"`
	Limit               int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	Offset              int32      `json:"offset,omitempty" minimum:"0"`
}

// CustomerList 是搜索到的顾客列表。
type CustomerList struct {
	Items []Customer `json:"items"`
}

// CustomerSourceList 是建档门店的顾客来源列表。
type CustomerSourceList struct {
	Items []CustomerSource `json:"items"`
}

// ListCustomerSourcesInput 是来源列表的空参数。
type ListCustomerSourcesInput struct{}

// Register 声明顾客的读写操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[CustomerIDInput, Customer]{
		Name: "get_customer", Summary: "查看顾客档案和安全提醒", Kind: ops.Read,
		Permission: perm.CustomerView, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *CustomerIDInput) (*Customer, error) {
			row, err := env.Q.GetCustomer(ctx, in.ID)
			if err != nil {
				return nil, rowError(err)
			}
			if !canView(env.Caller, row) {
				return nil, ops.ErrNotFound
			}
			out, err := view(ctx, env, row)
			if err != nil {
				return nil, err
			}
			return mask(env, out), nil
		},
	})
	ops.Register(r, ops.Operation[SearchCustomersInput, CustomerList]{
		Name: "search_customers", Summary: "按手机号、后四位或姓名查找顾客", Kind: ops.Read,
		Permission: perm.CustomerView, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *SearchCustomersInput) (*CustomerList, error) {
			limit := in.Limit
			if limit == 0 {
				limit = 50
			}
			self, err := viewScope(env.Caller)
			if err != nil {
				return nil, err
			}
			rows, err := env.Q.SearchCustomers(ctx, dbgen.SearchCustomersParams{Query: in.Query, IncludeUnidentified: in.IncludeUnidentified, ResponsibleStaffID: in.ResponsibleStaffID, SelfID: self, RowLimit: limit, RowOffset: in.Offset})
			if err != nil {
				return nil, err
			}
			out := &CustomerList{Items: []Customer{}}
			items, err := views(ctx, env, rows)
			if err != nil {
				return nil, err
			}
			for _, c := range items {
				out.Items = append(out.Items, *mask(env, c))
			}
			return out, nil
		},
	})
	ops.Register(r, ops.Operation[ListCustomerSourcesInput, CustomerSourceList]{
		Name: "list_customer_sources", Summary: "列出本门店的顾客来源", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListCustomerSourcesInput) (*CustomerSourceList, error) {
			rows, err := env.Q.ListCustomerSources(ctx, env.Caller.StoreID)
			if err != nil {
				return nil, err
			}
			out := &CustomerSourceList{Items: []CustomerSource{}}
			for _, row := range rows {
				out.Items = append(out.Items, sourceView(row))
			}
			return out, nil
		},
	})
	registerProfiles(r)
	registerMerge(r)
	registerSafety(r)
	registerAssignments(r)
	registerSources(r)
}
