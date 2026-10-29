package staff

import (
	"context"
	"errors"

	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// Me 是当前登录员工的信息和有效权限。前端按 permissions 决定显示哪些页面和按钮。
type Me struct {
	Staff       Staff       `json:"staff"`
	IsOwner     bool        `json:"is_owner"`
	Permissions []GrantSpec `json:"permissions" doc:"有效权限：各角色权限的并集，限制取最宽"`
}

type GetMeInput struct{}

type ChangeMyPasswordInput struct {
	OldPassword string `json:"old_password" minLength:"1" maxLength:"128"`
	NewPassword string `json:"new_password" minLength:"8" maxLength:"128"`
}

type OK struct {
	OK bool `json:"ok"`
}

func registerSelf(r *ops.Registry) {
	ops.Register(r, ops.Operation[GetMeInput, Me]{
		Name: "get_me", Summary: "查看我自己的档案、角色和权限", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *GetMeInput) (*Me, error) {
			st, err := view(ctx, env, env.Caller.StaffID)
			if err != nil {
				return nil, err
			}
			me := &Me{Staff: *st, IsOwner: env.Caller.Owner, Permissions: []GrantSpec{}}
			for _, d := range perm.Catalogue {
				if env.Caller.Has(d.Name) {
					me.Permissions = append(me.Permissions, specFromGrant(d.Name, env.Caller.Grant(d.Name)))
				}
			}
			return me, nil
		},
	})
	ops.Register(r, ops.Operation[ChangeMyPasswordInput, OK]{
		Name: "change_my_password", Summary: "修改我自己的登录密码", Kind: ops.Write,
		Permission: ops.LoggedIn, AI: ops.AINone,
		Handle: func(ctx context.Context, env ops.Env, in *ChangeMyPasswordInput) (*OK, error) {
			if err := auth.CheckPassword(ctx, env.Q, env.Caller.StaffID, in.OldPassword); err != nil {
				if errors.Is(err, auth.ErrInvalidLogin) {
					return nil, ops.CommitRule("原密码不正确")
				}
				if errors.Is(err, auth.ErrLoginLocked) {
					return nil, ops.CommitRule("密码错误次数过多，请 15 分钟后再试")
				}
				return nil, err
			}
			hash, err := auth.HashPassword(in.NewPassword)
			if err != nil {
				return nil, err
			}
			if _, err := env.Q.SetStaffPassword(ctx, dbgen.SetStaffPasswordParams{ID: env.Caller.StaffID, PasswordHash: &hash}); err != nil {
				return nil, err
			}
			return &OK{OK: true}, nil
		},
	})
}
