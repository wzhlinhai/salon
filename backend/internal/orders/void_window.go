package orders

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// VoidWindow 是本人在门店开启的撤单授权窗口。
type VoidWindow struct {
	ID        uuid.UUID `json:"id"`
	StaffID   uuid.UUID `json:"staff_id"`
	OpenedAt  time.Time `json:"opened_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// OpenVoidWindowInput 是开启撤单授权窗口时本人的登录密码。
type OpenVoidWindowInput struct {
	Password string `json:"password" minLength:"1" maxLength:"128"`
}

func registerVoidWindow(r *ops.Registry) {
	ops.Register(r, ops.Operation[OpenVoidWindowInput, VoidWindow]{Name: "open_void_window", Summary: "输入本人登录密码开启撤单授权窗口", Kind: ops.Write, Permission: perm.OrderVoid, AI: ops.AINone, Handle: openVoidWindow})
}

func openVoidWindow(ctx context.Context, env ops.Env, in *OpenVoidWindowInput) (*VoidWindow, error) {
	return openVoidWindowAt(ctx, env, in, time.Now())
}

func voidWindowMinutes(ctx context.Context, env ops.Env, now time.Time) (int, error) {
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return 0, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return 0, err
	}
	return rules.VoidWindowMinutes(), nil
}

func openVoidWindowAt(ctx context.Context, env ops.Env, in *OpenVoidWindowInput, now time.Time) (*VoidWindow, error) {
	minutes, err := voidWindowMinutes(ctx, env, now)
	if err != nil {
		return nil, err
	}
	if minutes == 0 {
		return nil, ops.Rule("撤单授权窗口未开启")
	}
	if err := auth.CheckPassword(ctx, env.Q, env.Caller.StaffID, in.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidLogin) {
			return nil, ops.CommitRule("密码不正确")
		}
		if errors.Is(err, auth.ErrLoginLocked) {
			return nil, ops.CommitRule("密码错误次数过多，请 15 分钟后再试")
		}
		return nil, err
	}
	row, err := env.Q.CreateVoidWindow(ctx, dbgen.CreateVoidWindowParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID,
		OpenedAt: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Duration(minutes) * time.Minute), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := &VoidWindow{ID: row.ID, StaffID: row.StaffID, OpenedAt: row.OpenedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if err := audit.Record(ctx, env, "open_void_window", audit.StaffTarget(env.Caller.StaffID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}
