package staff

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// BootstrapInput 是开店所需的信息。
type BootstrapInput struct {
	TenantName    string
	StoreName     string
	OwnerName     string
	OwnerNumber   string
	OwnerAccount  string
	OwnerPassword string
}

// BootstrapResult 是开店后创建的记录。
type BootstrapResult struct {
	TenantID uuid.UUID
	StoreID  uuid.UUID
	OwnerID  uuid.UUID
	// Roles 是角色名到 ID，包括老板和四个默认角色。
	Roles map[string]uuid.UUID
}

// Bootstrap 开一个新租户：建门店、老板角色、默认角色（ADR-0009 的初始权限）和第一名老板。
func Bootstrap(ctx context.Context, pool *pgxpool.Pool, in BootstrapInput) (BootstrapResult, error) {
	res := BootstrapResult{TenantID: uuid.New(), Roles: map[string]uuid.UUID{}}
	hash, err := auth.HashPassword(in.OwnerPassword)
	if err != nil {
		return res, err
	}
	err = db.InTenant(ctx, pool, res.TenantID, func(q *dbgen.Queries) error {
		if _, err := q.CreateTenant(ctx, dbgen.CreateTenantParams{ID: res.TenantID, Name: in.TenantName}); err != nil {
			return err
		}
		store, err := q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: res.TenantID, Name: in.StoreName})
		if err != nil {
			return err
		}
		res.StoreID = store.ID
		for _, name := range []string{"路过进店", "老客介绍", "员工带客", "美团", "抖音", "预约", "其他"} {
			if _, err := q.CreateCustomerSource(ctx, dbgen.CreateCustomerSourceParams{TenantID: res.TenantID, StoreID: store.ID, Name: name}); err != nil {
				return err
			}
		}
		owner, err := q.CreateRole(ctx, dbgen.CreateRoleParams{
			TenantID: res.TenantID, StoreID: store.ID, Name: perm.OwnerRoleName, IsOwner: true,
		})
		if err != nil {
			return err
		}
		res.Roles[owner.Name] = owner.ID
		env := ops.Env{Caller: ops.Caller{TenantID: res.TenantID, StoreID: store.ID}, Q: q}
		for _, d := range perm.DefaultRoles {
			role, err := q.CreateRole(ctx, dbgen.CreateRoleParams{TenantID: res.TenantID, StoreID: store.ID, Name: d.Name})
			if err != nil {
				return err
			}
			res.Roles[d.Name] = role.ID
			grants := map[ops.Permission]ops.Grant{}
			for p, g := range d.Grants {
				grants[p] = perm.Normalize(p, g)
			}
			if err := writeGrants(ctx, env, role.ID, grants); err != nil {
				return err
			}
		}
		st, err := q.CreateStaff(ctx, dbgen.CreateStaffParams{
			TenantID: res.TenantID, StoreID: store.ID, Name: in.OwnerName, Number: in.OwnerNumber,
			LoginAccount: &in.OwnerAccount, PasswordHash: &hash,
		})
		if err != nil {
			return uniqueViolation(err)
		}
		res.OwnerID = st.ID
		return q.AddStaffRole(ctx, dbgen.AddStaffRoleParams{
			TenantID: res.TenantID, StoreID: store.ID, StaffID: st.ID, RoleID: owner.ID,
		})
	})
	return res, err
}
