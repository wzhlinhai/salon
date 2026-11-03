package orders

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payments"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerVoid(r *ops.Registry) {
	registerVoidWindow(r)
	ops.Register(r, ops.Operation[VoidOrderInput, Order]{Name: "void_order", Summary: "撤已结账的单并退回卡余额、次数和红包", Kind: ops.Write, Permission: perm.OrderVoid, AI: ops.AIDraft, Handle: voidOrder})
}

func voidOrder(ctx context.Context, env ops.Env, in *VoidOrderInput) (*Order, error) {
	now := env.Now
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return nil, ops.Rule("请填写不超过200字的撤单原因")
	}
	minutes, err := voidWindowMinutes(ctx, env, now)
	if err != nil {
		return nil, err
	}
	if minutes > 0 {
		allowed, err := env.Q.HasVoidWindow(ctx, dbgen.HasVoidWindowParams{StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID, Now: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ops.Rule("请先输入登录密码开启撤单授权窗口")
		}
	}

	after, err := change(ctx, env, "void_order", "paid", []uuid.UUID{in.OrderID}, func(rows []dbgen.Order) error {
		row := &rows[0]
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		reversal, err := env.Q.CreateOrderVoid(ctx, dbgen.CreateOrderVoidParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderID: row.ID, Reason: reason, BusinessDate: pgtype.Date{Time: day, Valid: true}, VoidedBy: env.Caller.StaffID})
		if err != nil {
			return err
		}
		if err := payments.Reverse(ctx, env, row.ID, reversal.ID, day); err != nil {
			return err
		}
		if err := performance.ReverseOrder(ctx, env, row.ID, reversal.ID, day); err != nil {
			return err
		}
		if err := env.Q.MarkOrderVoided(ctx, dbgen.MarkOrderVoidedParams{StoreID: env.Caller.StoreID, ID: row.ID}); err != nil {
			return err
		}
		row.Status = "voided"
		return nil
	}, now)
	if err != nil {
		return nil, err
	}
	return &after[0], nil
}
