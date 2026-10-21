// Package staff 提供员工、级别、岗位、角色相关的业务操作（ADR-0009）。
package staff

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// Ref 是对级别、岗位或角色的引用。
type Ref struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Staff 是返回给调用者的员工信息。停用的员工仍然返回，历史单据靠它显示姓名。
type Staff struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name" doc:"姓名"`
	Number        string     `json:"number" doc:"员工编号"`
	Level         *Ref       `json:"level,omitempty" doc:"级别"`
	Position      *Ref       `json:"position,omitempty" doc:"岗位"`
	LoginAccount  *string    `json:"login_account,omitempty" doc:"登录账号"`
	Active        bool       `json:"active" doc:"是否在职"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
	Roles         []Ref      `json:"roles"`
}

// GrantSpec 是角色里的一项权限及其限制。
type GrantSpec struct {
	Permission string  `json:"permission" doc:"权限名，见 list_permissions"`
	Scope      *string `json:"scope,omitempty" enum:"self,store" doc:"数据范围：self 本人，store 全店。只对支持数据范围的权限有效"`
	WindowDays *int    `json:"window_days,omitempty" minimum:"0" doc:"只能处理多少天内的单：0 为当天，不填为不限。只对支持时间窗口的权限有效"`
}

// Role 是返回给调用者的角色。
type Role struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	IsOwner     bool        `json:"is_owner" doc:"老板角色拥有全部权限，不能修改"`
	Permissions []GrantSpec `json:"permissions"`
}

// Register 声明本包的全部操作。
func Register(r *ops.Registry) {
	registerSelf(r)
	registerStaff(r)
	registerRoles(r)
	registerLevels(r)
}

func specFromGrant(p ops.Permission, g ops.Grant) GrantSpec {
	s := GrantSpec{Permission: string(p), WindowDays: g.WindowDays}
	if g.Scope != "" {
		sc := string(g.Scope)
		s.Scope = &sc
	}
	return s
}

func grantFromSpec(s GrantSpec) (ops.Permission, ops.Grant, error) {
	p := ops.Permission(s.Permission)
	if _, ok := perm.Lookup(p); !ok {
		return "", ops.Grant{}, ops.Rule("没有这项权限：" + s.Permission)
	}
	g := ops.Grant{WindowDays: s.WindowDays}
	if s.Scope != nil {
		g.Scope = ops.Scope(*s.Scope)
	}
	return p, perm.Normalize(p, g), nil
}

// roleInfo 是一个角色及其权限。
type roleInfo struct {
	row    dbgen.Role
	grants map[ops.Permission]ops.Grant
}

// loadRoles 读取本门店的角色。ids 为空时读取全部。不属于本门店的 ID 返回 ErrNotFound。
func loadRoles(ctx context.Context, env ops.Env, ids []uuid.UUID) (map[uuid.UUID]*roleInfo, error) {
	rows, err := env.Q.ListRoles(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]*roleInfo{}
	var all []uuid.UUID
	for _, r := range rows {
		if len(ids) > 0 && !slices.Contains(ids, r.ID) {
			continue
		}
		out[r.ID] = &roleInfo{row: r, grants: map[ops.Permission]ops.Grant{}}
		all = append(all, r.ID)
	}
	for _, id := range ids {
		if out[id] == nil {
			return nil, ops.ErrNotFound
		}
	}
	perms, err := env.Q.ListRolePermissions(ctx, all)
	if err != nil {
		return nil, err
	}
	for _, p := range perms {
		name := ops.Permission(p.Permission)
		out[p.RoleID].grants[name] = perm.Normalize(name, auth.GrantFromRow(p))
	}
	return out, nil
}

// canGrant 报告调用者能否授予或收回这个角色：老板角色只有老板能动，其他角色不能超过调用者自己的权限。
func canGrant(c ops.Caller, owner bool, grants map[ops.Permission]ops.Grant) bool {
	if owner {
		return c.Owner
	}
	for p, g := range grants {
		if !c.Has(p) || !c.Grant(p).Covers(g) {
			return false
		}
	}
	return true
}

// ensureCanManage 检查调用者能否修改员工 target 的档案、账号或在职状态：
// target 的每个角色都必须是调用者能授予的，否则就能通过改密码等方式接管权限更大的账号。
func ensureCanManage(ctx context.Context, env ops.Env, target uuid.UUID) error {
	roles, err := env.Q.ListStaffRoles(ctx, target)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, len(roles))
	for i, r := range roles {
		ids[i] = r.ID
	}
	if len(ids) == 0 {
		return nil
	}
	infos, err := loadRoles(ctx, env, ids)
	if err != nil {
		return err
	}
	for _, info := range infos {
		if !canGrant(env.Caller, info.row.IsOwner, info.grants) {
			return ops.ErrForbidden
		}
	}
	return nil
}

// lockOwners 以本门店老板角色排队，串行员工管理、新建员工和角色修改，保护老板底线及授权检查。
func lockOwners(ctx context.Context, env ops.Env) error {
	_, err := env.Q.LockOwnerRole(ctx, env.Caller.StoreID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func ensureOwnerRemains(ctx context.Context, env ops.Env) error {
	n, err := env.Q.CountActiveOwners(ctx, env.Caller.StoreID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ops.Rule("至少要保留一名在职老板")
	}
	return nil
}

func getStaffRow(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.Staff, error) {
	st, err := env.Q.GetStaff(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && st.StoreID != env.Caller.StoreID) {
		return dbgen.Staff{}, ops.ErrNotFound
	}
	return st, err
}

// uniqueViolation 把唯一约束冲突转成面向人的业务错误。
func uniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}
	switch pgErr.ConstraintName {
	case "staff_store_id_number_key":
		return ops.Rule("员工编号已存在")
	case "staff_login_account_key":
		return ops.Rule("登录账号已被使用")
	case "roles_store_id_name_key":
		return ops.Rule("角色名称已存在")
	case "levels_store_id_name_key":
		return ops.Rule("级别名称已存在")
	case "positions_store_id_name_key":
		return ops.Rule("岗位名称已存在")
	}
	return err
}
