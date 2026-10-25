package orders

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerVoid(r *ops.Registry) {
	ops.Register(r, ops.Operation[VoidOrderInput, Order]{Name: "void_order", Summary: "撤已结账的单并退回卡余额、次数和红包", Kind: ops.Write, Permission: perm.OrderVoid, AI: ops.AIDraft, Handle: voidOrder})
}

func voidOrder(ctx context.Context, env ops.Env, in *VoidOrderInput) (*Order, error) {
	return voidOrderAt(ctx, env, in, time.Now())
}

func voidOrderAt(ctx context.Context, env ops.Env, in *VoidOrderInput, now time.Time) (*Order, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return nil, ops.Rule("请填写不超过200字的撤单原因")
	}
	after, err := change(ctx, env, "void_order", "paid", []uuid.UUID{in.OrderID}, func(rows []dbgen.Order) error {
		row := &rows[0]
		// 与顾客合并、结账同序：单 → 欠款 → 卡 → 红包；各组按 UUID，持锁后不回锁顾客。
		if err := debts.LockOrderForVoid(ctx, env, row.ID); err != nil {
			return err
		}
		payments, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{row.ID}})
		if err != nil {
			return err
		}
		cardIDs, packetIDs := []uuid.UUID{}, []uuid.UUID{}
		for _, p := range payments {
			if p.CardID != nil {
				cardIDs = append(cardIDs, *p.CardID)
			}
			if p.RedPacketID != nil {
				packetIDs = append(packetIDs, *p.RedPacketID)
			}
		}
		used, err := cards.LockRestores(ctx, env, cardIDs)
		if err != nil {
			return err
		}
		packets, err := redpackets.LockPayments(ctx, env, packetIDs)
		if err != nil {
			return err
		}
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		reversal, err := env.Q.CreateOrderVoid(ctx, dbgen.CreateOrderVoidParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderID: row.ID, Reason: reason, BusinessDate: pgtype.Date{Time: day, Valid: true}, VoidedBy: env.Caller.StaffID})
		if err != nil {
			return err
		}
		if err := env.Q.MarkOrderVoided(ctx, dbgen.MarkOrderVoidedParams{StoreID: env.Caller.StoreID, ID: row.ID}); err != nil {
			return err
		}
		row.Status = "voided"
		balances, err := env.Q.VoidBalanceConsumes(ctx, dbgen.VoidBalanceConsumesParams{StoreID: env.Caller.StoreID, OrderID: row.ID})
		if err != nil {
			return err
		}
		for _, consume := range balances {
			if err := used[consume.CardID].RestoreBalance(ctx, env, consume, reversal.ID, day); err != nil {
				return err
			}
		}
		counts, err := env.Q.VoidCountConsumes(ctx, dbgen.VoidCountConsumesParams{StoreID: env.Caller.StoreID, OrderID: row.ID})
		if err != nil {
			return err
		}
		for _, consume := range counts {
			if err := used[consume.CardID].RestoreCount(ctx, env, consume, reversal.ID, day); err != nil {
				return err
			}
		}
		for _, id := range packetIDs {
			if err := redpackets.Restore(ctx, env, packets[id], day); err != nil {
				return err
			}
		}
		return cards.AuditRestores(ctx, env, used)
	}, now)
	if err != nil {
		return nil, err
	}
	return &after[0], nil
}
