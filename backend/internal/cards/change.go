package cards

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type cardChange struct {
	row    dbgen.Card
	before *Card
}

// change 先按 UUID 锁住所有卡，再检查查看范围、读改前、执行修改并逐卡记日志。
// 业绩员工由调用方在卡之前锁住；营业日对整次修改只计算一次。
func change(ctx context.Context, env ops.Env, action string, ids []uuid.UUID, edit func([]cardChange, time.Time) error) ([]*Card, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	locked := make(map[uuid.UUID]dbgen.Card, len(ids))
	for _, id := range ordered {
		row, err := env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return nil, rowError(err)
		}
		locked[id] = row
	}
	changes := make([]cardChange, len(ids))
	for i, id := range ids {
		row := locked[id]
		if err := customers.CheckViewable(ctx, env, row.CustomerID); err != nil {
			return nil, err
		}
		before, err := cardView(ctx, env, id)
		if err != nil {
			return nil, err
		}
		changes[i] = cardChange{row: row, before: before}
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	if err := edit(changes, day); err != nil {
		return nil, err
	}
	out := make([]*Card, len(ids))
	for i, c := range changes {
		after, err := recordChange(ctx, env, action, c.row, c.before)
		if err != nil {
			return nil, err
		}
		out[i] = after
	}
	return out, nil
}

func changeOne(ctx context.Context, env ops.Env, action string, id uuid.UUID, edit func(cardChange, time.Time) error) (*Card, error) {
	out, err := change(ctx, env, action, []uuid.UUID{id}, func(changes []cardChange, day time.Time) error { return edit(changes[0], day) })
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// recordChange 也用于已持卡锁的自动升级，保留充值与自动升级各自的日志。
func recordChange(ctx context.Context, env ops.Env, action string, row dbgen.Card, before *Card) (*Card, error) {
	after, err := cardView(ctx, env, row.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, action, audit.CardTarget(row.ID, row.CustomerID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

// writeBalance 以同一种当前余额视图计算流水前值、差额和目标余额。
func writeBalance(ctx context.Context, env ops.Env, id uuid.UUID, kind string, principal, bonus money.Amount, principalDelta, bonusDelta money.Change, day time.Time) (dbgen.CardLedger, error) {
	pa, err := principal.Apply(principalDelta)
	if err != nil {
		return dbgen.CardLedger{}, ops.Rule("卡余额超过上限")
	}
	ba, err := bonus.Apply(bonusDelta)
	if err != nil {
		return dbgen.CardLedger{}, ops.Rule("卡余额超过上限")
	}
	return env.Q.AddCardLedger(ctx, dbgen.AddCardLedgerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: id, Kind: kind, PrincipalBefore: principal, PrincipalDelta: principalDelta, PrincipalAfter: pa, BonusBefore: bonus, BonusDelta: bonusDelta, BonusAfter: ba, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
}

// writePoints 将积分前值、差额和目标值收在同一处，充值与调整共用。
func writePoints(ctx context.Context, env ops.Env, in dbgen.AddCardPointsParams, before, after int32) error {
	if after < 0 || after > 99999999 {
		return ops.Rule("积分超过上限")
	}
	in.TenantID, in.StoreID, in.ActorID = env.Caller.TenantID, env.Caller.StoreID, env.Caller.StaffID
	in.Before, in.Delta, in.After = before, after-before, after
	return env.Q.AddCardPoints(ctx, in)
}
