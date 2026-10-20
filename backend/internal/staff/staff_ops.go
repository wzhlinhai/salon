package staff

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type StaffList struct {
	Staff []Staff `json:"staff"`
}

type ListStaffInput struct{}

type CreateStaffInput struct {
	Name       string      `json:"name" minLength:"1" maxLength:"50" doc:"姓名"`
	Number     string      `json:"number" minLength:"1" maxLength:"20" doc:"员工编号，本店内唯一"`
	LevelID    *uuid.UUID  `json:"level_id,omitempty" doc:"级别，见 list_levels"`
	PositionID *uuid.UUID  `json:"position_id,omitempty" doc:"岗位，见 list_positions"`
	RoleIDs    []uuid.UUID `json:"role_ids" doc:"角色，见 list_roles"`
}

type UpdateStaffInput struct {
	StaffID    uuid.UUID  `json:"staff_id"`
	Name       string     `json:"name" minLength:"1" maxLength:"50" doc:"姓名"`
	Number     string     `json:"number" minLength:"1" maxLength:"20" doc:"员工编号，本店内唯一"`
	LevelID    *uuid.UUID `json:"level_id,omitempty" doc:"级别，不填为不设"`
	PositionID *uuid.UUID `json:"position_id,omitempty" doc:"岗位，不填为不设"`
}

type SetStaffLoginInput struct {
	StaffID      uuid.UUID `json:"staff_id"`
	LoginAccount string    `json:"login_account" minLength:"3" maxLength:"50" pattern:"^[A-Za-z0-9_.@-]+$" doc:"登录账号，全系统唯一"`
	Password     string    `json:"password" minLength:"8" maxLength:"128" doc:"新密码"`
}

type StaffIDInput struct {
	StaffID uuid.UUID `json:"staff_id"`
}

type SetStaffRolesInput struct {
	StaffID uuid.UUID   `json:"staff_id"`
	RoleIDs []uuid.UUID `json:"role_ids" doc:"员工的全部角色；权限取各角色的并集"`
}

func registerStaff(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListStaffInput, StaffList]{
		Name: "list_staff", Summary: "列出本店员工，包括已停用的", Kind: ops.Read,
		Permission: perm.StaffManage, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *ListStaffInput) (*StaffList, error) {
			rows, err := env.Q.ListStaff(ctx, env.Caller.StoreID)
			if err != nil {
				return nil, err
			}
			list, err := views(ctx, env, rows)
			return &StaffList{Staff: list}, err
		},
	})
	ops.Register(r, ops.Operation[CreateStaffInput, Staff]{
		Name: "create_staff", Summary: "新建员工档案", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *CreateStaffInput) (*Staff, error) {
			if err := checkLevelPosition(ctx, env, in.LevelID, in.PositionID, nil); err != nil {
				return nil, err
			}
			st, err := env.Q.CreateStaff(ctx, dbgen.CreateStaffParams{
				TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
				Name: in.Name, Number: in.Number, LevelID: in.LevelID, PositionID: in.PositionID,
			})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			if err := setRoles(ctx, env, st.ID, in.RoleIDs); err != nil {
				return nil, err
			}
			after, err := view(ctx, env, st.ID)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "create_staff", st.ID, nil, after); err != nil {
				return nil, err
			}
			return after, nil
		},
	})
	ops.Register(r, ops.Operation[UpdateStaffInput, Staff]{
		Name: "update_staff", Summary: "修改员工的姓名、编号、级别、岗位", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *UpdateStaffInput) (*Staff, error) {
			current, err := lockStaffRow(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := ensureCanManage(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			before, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := checkLevelPosition(ctx, env, in.LevelID, in.PositionID, &current); err != nil {
				return nil, err
			}
			_, err = env.Q.UpdateStaffProfile(ctx, dbgen.UpdateStaffProfileParams{
				ID: in.StaffID, Name: in.Name, Number: in.Number, LevelID: in.LevelID, PositionID: in.PositionID,
			})
			if err != nil {
				return nil, uniqueViolation(err)
			}
			after, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "update_staff", in.StaffID, before, after); err != nil {
				return nil, err
			}
			return after, nil
		},
	})
	ops.Register(r, ops.Operation[SetStaffLoginInput, Staff]{
		Name: "set_staff_login", Summary: "设置员工的登录账号和密码", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AINone,
		Handle: func(ctx context.Context, env ops.Env, in *SetStaffLoginInput) (*Staff, error) {
			if _, err := lockStaffRow(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			if err := ensureCanManage(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			before, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			hash, err := auth.HashPassword(in.Password)
			if err != nil {
				return nil, err
			}
			if _, err := env.Q.SetStaffLogin(ctx, dbgen.SetStaffLoginParams{
				ID: in.StaffID, LoginAccount: &in.LoginAccount, PasswordHash: &hash,
			}); err != nil {
				return nil, uniqueViolation(err)
			}
			after, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "set_staff_login", in.StaffID, before, after); err != nil {
				return nil, err
			}
			return after, nil
		},
	})
	ops.Register(r, ops.Operation[StaffIDInput, Staff]{
		Name: "deactivate_staff", Summary: "员工离职：停用，不删除；停用后不能登录，历史单据仍显示姓名", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *StaffIDInput) (*Staff, error) {
			if _, err := lockStaffRow(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			if err := ensureCanManage(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			before, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := lockOwners(ctx, env); err != nil {
				return nil, err
			}
			n, err := env.Q.DeactivateStaff(ctx, in.StaffID)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, ops.Rule("员工已经是停用状态")
			}
			if err := ensureOwnerRemains(ctx, env); err != nil {
				return nil, err
			}
			after, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "deactivate_staff", in.StaffID, before, after); err != nil {
				return nil, err
			}
			return after, nil
		},
	})
	ops.Register(r, ops.Operation[StaffIDInput, Staff]{
		Name: "reactivate_staff", Summary: "恢复已停用的员工", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *StaffIDInput) (*Staff, error) {
			if _, err := lockStaffRow(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			if err := ensureCanManage(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			before, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			n, err := env.Q.ReactivateStaff(ctx, in.StaffID)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, ops.Rule("员工已经在职")
			}
			after, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "reactivate_staff", in.StaffID, before, after); err != nil {
				return nil, err
			}
			return after, nil
		},
	})
	ops.Register(r, ops.Operation[SetStaffRolesInput, Staff]{
		Name: "set_staff_roles", Summary: "设置员工的角色", Kind: ops.Write,
		Permission: perm.StaffManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *SetStaffRolesInput) (*Staff, error) {
			if _, err := lockStaffRow(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			if err := ensureCanManage(ctx, env, in.StaffID); err != nil {
				return nil, err
			}
			before, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := lockOwners(ctx, env); err != nil {
				return nil, err
			}
			if err := setRoles(ctx, env, in.StaffID, in.RoleIDs); err != nil {
				return nil, err
			}
			if err := ensureOwnerRemains(ctx, env); err != nil {
				return nil, err
			}
			after, err := view(ctx, env, in.StaffID)
			if err != nil {
				return nil, err
			}
			if err := audit.Record(ctx, env, "set_staff_roles", in.StaffID, before, after); err != nil {
				return nil, err
			}
			return after, nil
		},
	})
}

// setRoles 把员工的角色设为 ids。增加或去掉的每个角色都必须是调用者能授予的。
func setRoles(ctx context.Context, env ops.Env, staffID uuid.UUID, ids []uuid.UUID) error {
	ids = dedupe(ids)
	current, err := env.Q.ListStaffRoles(ctx, staffID)
	if err != nil {
		return err
	}
	var changed []uuid.UUID
	for _, id := range ids {
		if !slices.ContainsFunc(current, func(r dbgen.Role) bool { return r.ID == id }) {
			changed = append(changed, id)
		}
	}
	for _, r := range current {
		if !slices.Contains(ids, r.ID) {
			changed = append(changed, r.ID)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	infos, err := loadRoles(ctx, env, changed)
	if err != nil {
		return err
	}
	for _, info := range infos {
		if !canGrant(env.Caller, info.row.IsOwner, info.grants) {
			return ops.ErrForbidden
		}
	}
	if err := env.Q.DeleteStaffRoles(ctx, staffID); err != nil {
		return err
	}
	for _, id := range ids {
		if err := env.Q.AddStaffRole(ctx, dbgen.AddStaffRoleParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: staffID, RoleID: id,
		}); err != nil {
			return err
		}
	}
	return nil
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// checkLevelPosition 检查级别和岗位属于本门店且处于启用状态。员工已有的级别或岗位即使已停用也可以保留。
func checkLevelPosition(ctx context.Context, env ops.Env, level, position *uuid.UUID, current *dbgen.Staff) error {
	keeps := func(id *uuid.UUID, cur func(dbgen.Staff) *uuid.UUID) bool {
		return current != nil && cur(*current) != nil && *cur(*current) == *id
	}
	if level != nil && !keeps(level, func(s dbgen.Staff) *uuid.UUID { return s.LevelID }) {
		levels, err := env.Q.ListLevels(ctx, env.Caller.StoreID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(levels, func(l dbgen.Level) bool { return l.ID == *level && l.Active }) {
			return ops.Rule("没有这个级别，或者它已停用")
		}
	}
	if position != nil && !keeps(position, func(s dbgen.Staff) *uuid.UUID { return s.PositionID }) {
		positions, err := env.Q.ListPositions(ctx, env.Caller.StoreID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(positions, func(p dbgen.Position) bool { return p.ID == *position && p.Active }) {
			return ops.Rule("没有这个岗位，或者它已停用")
		}
	}
	return nil
}

func view(ctx context.Context, env ops.Env, id uuid.UUID) (*Staff, error) {
	st, err := getStaffRow(ctx, env, id)
	if err != nil {
		return nil, err
	}
	list, err := views(ctx, env, []dbgen.Staff{st})
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}

// views 把员工记录转成返回值，带上级别、岗位、角色的名称。
func views(ctx context.Context, env ops.Env, rows []dbgen.Staff) ([]Staff, error) {
	store := env.Caller.StoreID
	levels, err := env.Q.ListLevels(ctx, store)
	if err != nil {
		return nil, err
	}
	positions, err := env.Q.ListPositions(ctx, store)
	if err != nil {
		return nil, err
	}
	roles, err := env.Q.ListRoles(ctx, store)
	if err != nil {
		return nil, err
	}
	links, err := env.Q.ListAllStaffRoles(ctx, store)
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, l := range levels {
		names[l.ID] = l.Name
	}
	for _, p := range positions {
		names[p.ID] = p.Name
	}
	ref := func(id *uuid.UUID) *Ref {
		if id == nil {
			return nil
		}
		return &Ref{ID: *id, Name: names[*id]}
	}
	out := make([]Staff, len(rows))
	for i, st := range rows {
		s := Staff{
			ID: st.ID, Name: st.Name, Number: st.Number,
			Level: ref(st.LevelID), Position: ref(st.PositionID),
			LoginAccount: st.LoginAccount, Active: st.Active, Roles: []Ref{},
		}
		if st.DeactivatedAt.Valid {
			s.DeactivatedAt = &st.DeactivatedAt.Time
		}
		for _, r := range roles {
			if slices.ContainsFunc(links, func(l dbgen.ListAllStaffRolesRow) bool {
				return l.StaffID == st.ID && l.RoleID == r.ID
			}) {
				s.Roles = append(s.Roles, Ref{ID: r.ID, Name: r.Name})
			}
		}
		out[i] = s
	}
	return out, nil
}
