package staff

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// Option 是级别或岗位。停用后不能再分配给员工，已分配的保留。
type Option struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

type OptionList struct {
	Items []Option `json:"items"`
}

type CreateOptionInput struct {
	Name string `json:"name" minLength:"1" maxLength:"20"`
}

type UpdateOptionInput struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name" minLength:"1" maxLength:"20"`
	Active bool      `json:"active"`
}

func registerLevels(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListInput, OptionList]{
		Name: "list_levels", Summary: "列出本店的员工级别（例如首席、总监）", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListInput) (*OptionList, error) {
			rows, err := env.Q.ListLevels(ctx, env.Caller.StoreID)
			out := &OptionList{Items: []Option{}}
			for _, l := range rows {
				out.Items = append(out.Items, Option{l.ID, l.Name, l.Active})
			}
			return out, err
		},
	})
	ops.Register(r, ops.Operation[CreateOptionInput, Option]{
		Name: "create_level", Summary: "新建员工级别", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreateOptionInput) (*Option, error) {
			l, err := env.Q.CreateLevel(ctx, dbgen.CreateLevelParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return &Option{l.ID, l.Name, l.Active}, nil
		},
	})
	ops.Register(r, ops.Operation[UpdateOptionInput, Option]{
		Name: "update_level", Summary: "修改或停用员工级别", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateOptionInput) (*Option, error) {
			l, err := env.Q.UpdateLevel(ctx, dbgen.UpdateLevelParams{ID: in.ID, Name: in.Name, Active: in.Active})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && l.StoreID != env.Caller.StoreID) {
				return nil, ops.ErrNotFound
			}
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return &Option{l.ID, l.Name, l.Active}, nil
		},
	})
	ops.Register(r, ops.Operation[ListInput, OptionList]{
		Name: "list_positions", Summary: "列出本店的岗位（例如大工、助理），岗位用于计算提成", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListInput) (*OptionList, error) {
			rows, err := env.Q.ListPositions(ctx, env.Caller.StoreID)
			out := &OptionList{Items: []Option{}}
			for _, p := range rows {
				out.Items = append(out.Items, Option{p.ID, p.Name, p.Active})
			}
			return out, err
		},
	})
	ops.Register(r, ops.Operation[CreateOptionInput, Option]{
		Name: "create_position", Summary: "新建岗位", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreateOptionInput) (*Option, error) {
			p, err := env.Q.CreatePosition(ctx, dbgen.CreatePositionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return &Option{p.ID, p.Name, p.Active}, nil
		},
	})
	ops.Register(r, ops.Operation[UpdateOptionInput, Option]{
		Name: "update_position", Summary: "修改或停用岗位", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateOptionInput) (*Option, error) {
			p, err := env.Q.UpdatePosition(ctx, dbgen.UpdatePositionParams{ID: in.ID, Name: in.Name, Active: in.Active})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.StoreID != env.Caller.StoreID) {
				return nil, ops.ErrNotFound
			}
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return &Option{p.ID, p.Name, p.Active}, nil
		},
	})
}
