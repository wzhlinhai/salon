// Package stores 提供门店相关的业务操作。
package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// Store 是返回给调用者的门店信息。
type Store struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name" doc:"门店名称"`
	CreatedAt time.Time `json:"created_at"`
}

type GetStoreInput struct{}

type RenameStoreInput struct {
	Name string `json:"name" minLength:"1" maxLength:"100" doc:"新的门店名称"`
}

// Register 声明门店相关的操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[GetStoreInput, Store]{
		Name:       "get_store",
		Summary:    "查看当前门店的信息",
		Kind:       ops.Read,
		Permission: ops.LoggedIn,
		AI:         ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *GetStoreInput) (*Store, error) {
			return currentStore(ctx, env)
		},
	})
	ops.Register(r, ops.Operation[RenameStoreInput, Store]{
		Name:       "rename_store",
		Summary:    "修改当前门店的名称",
		Kind:       ops.Write,
		Permission: perm.SettingsManage,
		AI:         ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *RenameStoreInput) (*Store, error) {
			n, err := env.Q.RenameStore(ctx, dbgen.RenameStoreParams{ID: env.Caller.StoreID, Name: in.Name})
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, ops.ErrNotFound
			}
			return currentStore(ctx, env)
		},
	})
}

func currentStore(ctx context.Context, env ops.Env) (*Store, error) {
	s, err := env.Q.GetStore(ctx, env.Caller.StoreID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Store{ID: s.ID, Name: s.Name, CreatedAt: s.CreatedAt.Time}, nil
}
