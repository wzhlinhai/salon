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

// Level 是员工级别（例如首席、总监）。停用后不能再分配给员工，已分配的保留。
type Level struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

// LevelList 是本店的全部级别。
type LevelList struct {
	Items []Level `json:"items"`
}

// Position 是岗位（例如大工、助理），用于计算提成。停用后不能再分配给员工，已分配的保留。
type Position struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

// PositionList 是本店的全部岗位。
type PositionList struct {
	Items []Position `json:"items"`
}

// CreateLevelInput 是新建级别的参数。
type CreateLevelInput struct {
	Name string `json:"name" minLength:"1" maxLength:"20"`
}

// UpdateLevelInput 是修改或停用级别的参数。
type UpdateLevelInput struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name" minLength:"1" maxLength:"20"`
	Active bool      `json:"active"`
}

// CreatePositionInput 是新建岗位的参数。
type CreatePositionInput struct {
	Name string `json:"name" minLength:"1" maxLength:"20"`
}

// UpdatePositionInput 是修改或停用岗位的参数。
type UpdatePositionInput struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name" minLength:"1" maxLength:"20"`
	Active bool      `json:"active"`
}

func levelView(l dbgen.Level) Level { return Level{ID: l.ID, Name: l.Name, Active: l.Active} }

func positionView(p dbgen.Position) Position {
	return Position{ID: p.ID, Name: p.Name, Active: p.Active}
}

func registerLevels(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListInput, LevelList]{
		Name: "list_levels", Summary: "列出本店的员工级别（例如首席、总监）", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListInput) (*LevelList, error) {
			rows, err := env.Q.ListLevels(ctx, env.Caller.StoreID)
			out := &LevelList{Items: []Level{}}
			for _, l := range rows {
				out.Items = append(out.Items, levelView(l))
			}
			return out, err
		},
	})
	ops.Register(r, ops.Operation[CreateLevelInput, Level]{
		Name: "create_level", Summary: "新建员工级别", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreateLevelInput) (*Level, error) {
			l, err := env.Q.CreateLevel(ctx, dbgen.CreateLevelParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return new(levelView(l)), nil
		},
	})
	ops.Register(r, ops.Operation[UpdateLevelInput, Level]{
		Name: "update_level", Summary: "修改或停用员工级别", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateLevelInput) (*Level, error) {
			l, err := env.Q.UpdateLevel(ctx, dbgen.UpdateLevelParams{ID: in.ID, Name: in.Name, Active: in.Active})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && l.StoreID != env.Caller.StoreID) {
				return nil, ops.ErrNotFound
			}
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return new(levelView(l)), nil
		},
	})
	ops.Register(r, ops.Operation[ListInput, PositionList]{
		Name: "list_positions", Summary: "列出本店的岗位（例如大工、助理），岗位用于计算提成", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListInput) (*PositionList, error) {
			rows, err := env.Q.ListPositions(ctx, env.Caller.StoreID)
			out := &PositionList{Items: []Position{}}
			for _, p := range rows {
				out.Items = append(out.Items, positionView(p))
			}
			return out, err
		},
	})
	ops.Register(r, ops.Operation[CreatePositionInput, Position]{
		Name: "create_position", Summary: "新建岗位", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreatePositionInput) (*Position, error) {
			p, err := env.Q.CreatePosition(ctx, dbgen.CreatePositionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return new(positionView(p)), nil
		},
	})
	ops.Register(r, ops.Operation[UpdatePositionInput, Position]{
		Name: "update_position", Summary: "修改或停用岗位", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdatePositionInput) (*Position, error) {
			p, err := env.Q.UpdatePosition(ctx, dbgen.UpdatePositionParams{ID: in.ID, Name: in.Name, Active: in.Active})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.StoreID != env.Caller.StoreID) {
				return nil, ops.ErrNotFound
			}
			if err != nil {
				return nil, uniqueViolation(err)
			}
			return new(positionView(p)), nil
		},
	})
}
