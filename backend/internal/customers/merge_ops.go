package customers

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
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
			ids := []uuid.UUID{in.KeepID, in.MergedID}
			slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
			rows := map[uuid.UUID]dbgen.Customer{}
			for _, id := range ids {
				row, err := lock(ctx, env, id)
				if err != nil {
					return nil, err
				}
				rows[id] = row
			}
			keep, merged := rows[in.KeepID], rows[in.MergedID]
			beforeKeep, err := view(ctx, env, keep)
			if err != nil {
				return nil, err
			}
			beforeMerged, err := view(ctx, env, merged)
			if err != nil {
				return nil, err
			}
			if keep.Name == nil || *keep.Name == "" {
				keep.Name = merged.Name
			}
			if keep.Phone == nil && merged.Phone != nil {
				if err := env.Q.LockCustomerPhone(ctx, *merged.Phone); err != nil {
					return nil, err
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
			if err := save(ctx, env, merged); err != nil {
				return nil, err
			}
			if err := save(ctx, env, keep); err != nil {
				return nil, err
			}
			for _, ref := range customerReferences {
				if err := ref.move(ctx, env.Q, merged.ID, keep.ID); err != nil {
					return nil, err
				}
			}
			afterKeep, err := view(ctx, env, keep)
			if err != nil {
				return nil, err
			}
			afterMerged, err := view(ctx, env, merged)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "merge_customers", keep.ID, beforeKeep, afterKeep); err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "merge_customers", merged.ID, beforeMerged, afterMerged); err != nil {
				return nil, err
			}
			return &MergeCustomersResult{Keep: *mask(env, afterKeep), Merged: *mask(env, afterMerged)}, nil
		},
	})
}
