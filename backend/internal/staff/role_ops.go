package staff

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type PermissionList struct {
	Permissions []perm.Def `json:"permissions"`
}

type RoleList struct {
	Roles []Role `json:"roles"`
}

type ListInput struct{}

type CreateRoleInput struct {
	Name        string      `json:"name" minLength:"1" maxLength:"20" doc:"角色名称"`
	Permissions []GrantSpec `json:"permissions"`
}

type UpdateRoleInput struct {
	RoleID      uuid.UUID   `json:"role_id"`
	Name        string      `json:"name" minLength:"1" maxLength:"20" doc:"角色名称"`
	Permissions []GrantSpec `json:"permissions" doc:"角色的全部权限，替换原有权限"`
}

type RoleIDInput struct {
	RoleID uuid.UUID `json:"role_id"`
}

func registerRoles(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListInput, PermissionList]{
		Name: "list_permissions", Summary: "列出系统的全部权限及其可用的限制", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(context.Context, ops.Env, *ListInput) (*PermissionList, error) {
			return &PermissionList{Permissions: perm.Catalogue}, nil
		},
	})
	ops.Register(r, ops.Operation[ListInput, RoleList]{
		Name: "list_roles", Summary: "列出本店的角色及各自的权限", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListInput) (*RoleList, error) {
			infos, err := loadRoles(ctx, env, nil)
			if err != nil {
				return nil, err
			}
			rows, err := env.Q.ListRoles(ctx, env.Caller.StoreID)
			if err != nil {
				return nil, err
			}
			out := &RoleList{Roles: make([]Role, 0, len(rows))}
			for _, row := range rows {
				out.Roles = append(out.Roles, roleView(infos[row.ID]))
			}
			return out, nil
		},
	})
	ops.Register(r, ops.Operation[CreateRoleInput, Role]{
		Name: "create_role", Summary: "新建角色", Kind: ops.Write,
		Permission: perm.RoleManage, AI: ops.AINone,
		Handle: func(ctx context.Context, env ops.Env, in *CreateRoleInput) (*Role, error) {
			grants, err := parseGrants(env.Caller, in.Permissions)
			if err != nil {
				return nil, err
			}
			row, err := env.Q.CreateRole(ctx, dbgen.CreateRoleParams{
				TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: in.Name,
			})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			if err := writeGrants(ctx, env, row.ID, grants); err != nil {
				return nil, err
			}
			return roleByID(ctx, env, row.ID)
		},
	})
	ops.Register(r, ops.Operation[UpdateRoleInput, Role]{
		Name: "update_role", Summary: "修改角色的名称和权限", Kind: ops.Write,
		Permission: perm.RoleManage, AI: ops.AINone,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateRoleInput) (*Role, error) {
			info, err := editableRole(ctx, env, in.RoleID)
			if err != nil {
				return nil, err
			}
			grants, err := parseGrants(env.Caller, in.Permissions)
			if err != nil {
				return nil, err
			}
			if _, err := env.Q.RenameRole(ctx, dbgen.RenameRoleParams{ID: info.row.ID, Name: in.Name}); err != nil {
				return nil, uniqueViolation(err)
			}
			if err := env.Q.DeleteRolePermissions(ctx, info.row.ID); err != nil {
				return nil, err
			}
			if err := writeGrants(ctx, env, info.row.ID, grants); err != nil {
				return nil, err
			}
			return roleByID(ctx, env, info.row.ID)
		},
	})
	ops.Register(r, ops.Operation[RoleIDInput, OK]{
		Name: "delete_role", Summary: "删除角色；拥有该角色的员工失去这个角色", Kind: ops.Write,
		Permission: perm.RoleManage, AI: ops.AINone,
		Handle: func(ctx context.Context, env ops.Env, in *RoleIDInput) (*OK, error) {
			info, err := editableRole(ctx, env, in.RoleID)
			if err != nil {
				return nil, err
			}
			if _, err := env.Q.DeleteRole(ctx, info.row.ID); err != nil {
				return nil, err
			}
			return &OK{OK: true}, nil
		},
	})
}

// editableRole 返回可以由调用者修改的角色：不是老板角色，且现有权限不超过调用者。
func editableRole(ctx context.Context, env ops.Env, id uuid.UUID) (*roleInfo, error) {
	infos, err := loadRoles(ctx, env, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	info := infos[id]
	if info.row.IsOwner {
		return nil, ops.Rule("老板角色拥有全部权限，不能修改或删除")
	}
	if !canGrant(env.Caller, false, info.grants) {
		return nil, ops.ErrForbidden
	}
	return info, nil
}

// parseGrants 校验权限列表：权限存在、不重复、不超过调用者自己的权限。
func parseGrants(c ops.Caller, specs []GrantSpec) (map[ops.Permission]ops.Grant, error) {
	out := map[ops.Permission]ops.Grant{}
	for _, s := range specs {
		p, g, err := grantFromSpec(s)
		if err != nil {
			return nil, err
		}
		if _, dup := out[p]; dup {
			return nil, ops.Rule("权限重复：" + s.Permission)
		}
		out[p] = g
	}
	if !canGrant(c, false, out) {
		return nil, ops.ErrForbidden
	}
	return out, nil
}

func writeGrants(ctx context.Context, env ops.Env, roleID uuid.UUID, grants map[ops.Permission]ops.Grant) error {
	for p, g := range grants {
		params := dbgen.AddRolePermissionParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, RoleID: roleID, Permission: string(p),
		}
		if g.Scope != "" {
			sc := string(g.Scope)
			params.Scope = &sc
		}
		if g.WindowDays != nil {
			n := int32(*g.WindowDays)
			params.WindowDays = &n
		}
		if err := env.Q.AddRolePermission(ctx, params); err != nil {
			return err
		}
	}
	return nil
}

func roleByID(ctx context.Context, env ops.Env, id uuid.UUID) (*Role, error) {
	infos, err := loadRoles(ctx, env, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	r := roleView(infos[id])
	return &r, nil
}

func roleView(info *roleInfo) Role {
	r := Role{ID: info.row.ID, Name: info.row.Name, IsOwner: info.row.IsOwner, Permissions: []GrantSpec{}}
	for p, g := range info.grants {
		r.Permissions = append(r.Permissions, specFromGrant(p, g))
	}
	order := map[string]int{}
	for i, d := range perm.Catalogue {
		order[string(d.Name)] = i
	}
	sort.Slice(r.Permissions, func(i, j int) bool {
		return order[r.Permissions[i].Permission] < order[r.Permissions[j].Permission]
	})
	return r
}
