package customers

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// MergeCustomersInput 是保留及并入的两位顾客。
type MergeCustomersInput struct {
	KeepID   uuid.UUID `json:"keep_id"`
	MergedID uuid.UUID `json:"merged_id"`
}

// MergeCustomersResult 是合并后的两份档案。
type MergeCustomersResult struct {
	Keep   Customer `json:"keep"`
	Merged Customer `json:"merged"`
}

type customerReference struct {
	table, column string
	move          func(context.Context, *dbgen.Queries, uuid.UUID, uuid.UUID) error
}

// customerReferences 是合并时必须改挂的外键清单；新增单、卡等外键时在这里追加。
var customerReferences = []customerReference{
	{"orders", "customer_id", func(ctx context.Context, q *dbgen.Queries, from, to uuid.UUID) error {
		return q.MergeCustomerOrders(ctx, dbgen.MergeCustomerOrdersParams{MergedID: from, KeepID: to})
	}},
	{"cards", "customer_id", func(ctx context.Context, q *dbgen.Queries, from, to uuid.UUID) error {
		// 与 transfer_card_balance 一样按卡 ID 加锁，避免合并与转余额交错死锁。
		if _, err := q.LockMergedCustomerCards(ctx, from); err != nil {
			return err
		}
		return q.MergeCustomerCards(ctx, dbgen.MergeCustomerCardsParams{MergedID: from, KeepID: to})
	}},
	{"customer_safety_labels", "customer_id", func(ctx context.Context, q *dbgen.Queries, from, to uuid.UUID) error {
		return q.MergeCustomerSafetyLabels(ctx, dbgen.MergeCustomerSafetyLabelsParams{MergedID: from, KeepID: to})
	}},
}

func registerMerge(r *ops.Registry) {
	ops.Register(r, ops.Operation[MergeCustomersInput, MergeCustomersResult]{
		Name: "merge_customers", Summary: "不可撤销地合并重复顾客及其关联记录", Kind: ops.Write,
		Permission: perm.CustomerMerge, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *MergeCustomersInput) (*MergeCustomersResult, error) {
			if in.KeepID == in.MergedID {
				return nil, ops.Rule("不能合并同一位顾客")
			}
			items, err := change(ctx, env, "merge_customers", byIDs(in.MergedID, in.KeepID), func(rows []dbgen.Customer) error {
				merged, keep := &rows[0], &rows[1]
				if keep.Name == nil || *keep.Name == "" {
					keep.Name = merged.Name
				}
				if keep.Phone == nil && merged.Phone != nil {
					if err := env.Q.LockCustomerPhone(ctx, *merged.Phone); err != nil {
						return err
					}
					keep.Phone = merged.Phone
					merged.Phone = nil
				}
				if keep.Gender == nil {
					keep.Gender = merged.Gender
				}
				if keep.SourceID == nil {
					keep.SourceID = merged.SourceID
				}
				if keep.ResponsibleStaffID == nil {
					keep.ResponsibleStaffID = merged.ResponsibleStaffID
				}
				if keep.Note == nil || *keep.Note == "" {
					keep.Note = merged.Note
				}
				merged.MergedInto = &keep.ID
				for _, ref := range customerReferences {
					if err := ref.move(ctx, env.Q, merged.ID, keep.ID); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			return &MergeCustomersResult{Keep: *items[1], Merged: *items[0]}, nil
		},
	})
}
