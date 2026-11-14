package groupbuy

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Redemption 是本店核销记录，不包含顾客或订单写入。
type Redemption struct {
	ID             uuid.UUID     `json:"id"`
	Platform       string        `json:"platform"`
	CouponCode     string        `json:"coupon_code"`
	Key            string        `json:"key"`
	Status         string        `json:"status"`
	RedeemedAt     *time.Time    `json:"redeemed_at"`
	RevokedAt      *time.Time    `json:"revoked_at"`
	RedeemedBy     uuid.UUID     `json:"redeemed_by"`
	OrderID        *uuid.UUID    `json:"order_id" nullable:"true"`
	OrderPaymentID *uuid.UUID    `json:"order_payment_id" nullable:"true"`
	DealID         *uuid.UUID    `json:"deal_id" nullable:"true"`
	PurchasePrice  *money.Amount `json:"purchase_price" nullable:"true"`
	StorePrice     *money.Amount `json:"store_price" nullable:"true"`
	LinkedAt       *time.Time    `json:"linked_at" nullable:"true"`
	CreatedAt      time.Time     `json:"created_at"`
}

// LookupInput 是本店核销记录的幂等键。
type LookupInput struct {
	Key string `json:"key" minLength:"1" maxLength:"200"`
}

func view(row dbgen.CouponRedemption) *Redemption {
	out := &Redemption{ID: row.ID, Platform: row.Platform, CouponCode: row.CouponCode, Key: row.IdempotencyKey, Status: row.Status, RedeemedBy: row.RedeemedBy, CreatedAt: row.CreatedAt.Time}
	if row.RedeemedAt.Valid {
		out.RedeemedAt = &row.RedeemedAt.Time
	}
	if row.RevokedAt.Valid {
		out.RevokedAt = &row.RevokedAt.Time
	}
	out.OrderID = row.OrderID
	out.OrderPaymentID = row.OrderPaymentID
	out.DealID = row.DealID
	out.PurchasePrice = row.PurchasePrice
	out.StorePrice = row.StorePrice
	if row.LinkedAt.Valid {
		out.LinkedAt = &row.LinkedAt.Time
	}
	return out
}

// Register 声明仅员工可发起的平台动作和 AI 可读的本店记录查询。
func Register(r *ops.Registry, providers *Providers) {
	registerDeals(r)
	registerSettlements(r)
	registerLink(r)
	registerOpenGroupbuyOrder(r)
	ops.Register(r, ops.Operation[CouponRequest, CouponResult]{Name: "verify_coupon", Summary: "验团购券", Kind: ops.Read, Permission: perm.GroupBuyRedeem, AI: ops.AINone, Phased: true,
		Handle: func(ctx context.Context, env ops.Env, in *CouponRequest) (*CouponResult, error) {
			in.TenantID = env.Caller.TenantID
			in.StoreID = env.Caller.StoreID
			p := providers.Get(in.Platform)
			if p == nil {
				return nil, ops.Rule("团购平台未配置")
			}
			result, err := p.Verify(ctx, *in)
			if err != nil {
				return nil, platformError(err)
			}
			err = env.Transact(ctx, func(q *dbgen.Queries) error {
				env.Q = q
				return audit.Record(ctx, env, "verify_coupon", audit.CouponRedemptionTarget(uuid.New()), nil, result)
			})
			return &result, err
		}})
	ops.Register(r, ops.Operation[CouponRequest, Redemption]{Name: "redeem_coupon", Summary: "人工核销团购券", Kind: ops.Write, Permission: perm.GroupBuyRedeem, AI: ops.AINone, Phased: true,
		Handle: func(ctx context.Context, env ops.Env, in *CouponRequest) (*Redemption, error) {
			return redeem(ctx, env, *in, providers)
		}})
	ops.Register(r, ops.Operation[LookupInput, Redemption]{Name: "revoke_coupon", Summary: "撤销团购核销", Kind: ops.Write, Permission: perm.GroupBuyRedeem, AI: ops.AINone, Phased: true,
		Handle: func(ctx context.Context, env ops.Env, in *LookupInput) (*Redemption, error) {
			return revoke(ctx, env, *in, providers)
		}})
	ops.Register(r, ops.Operation[LookupInput, Redemption]{Name: "get_redemption", Summary: "查询本店团购核销记录", Kind: ops.Read, Permission: perm.GroupBuyRedeem, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *LookupInput) (*Redemption, error) {
			row, err := env.Q.GetRedemption(ctx, dbgen.GetRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: in.Key})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ops.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			return view(row), nil
		}})
	ops.Register(r, ops.Operation[ListRedemptionsInput, RedemptionList]{Name: "list_redemptions", Summary: "按营业日范围列出本店团购核销记录", Kind: ops.Read, Permission: perm.GroupBuyRedeem, AI: ops.AIDirect, Handle: listRedemptions})
}

// listRedemptions 按营业日范围返回核销记录；营业日由核销时刻换算，待核销记录按创建时刻。
func listRedemptions(ctx context.Context, env ops.Env, in *ListRedemptionsInput) (*RedemptionList, error) {
	query, err := daterange.Parse(env.Caller, perm.GroupBuyRedeem, in.From, in.To, nil, 0)
	if err != nil {
		return nil, err
	}
	from, to := query.From, query.To
	fromTime := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, settings.Location).AddDate(0, 0, -2)
	toTime := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, settings.Location).AddDate(0, 0, 3)
	rows, err := env.Q.ListRedemptions(ctx, dbgen.ListRedemptionsParams{StoreID: env.Caller.StoreID, FromTime: pgtype.Timestamptz{Time: fromTime, Valid: true}, ToTime: pgtype.Timestamptz{Time: toTime, Valid: true}})
	if err != nil {
		return nil, err
	}
	times := make([]time.Time, len(rows))
	for i, row := range rows {
		if row.RedeemedAt.Valid {
			times[i] = row.RedeemedAt.Time
		} else {
			times[i] = row.CreatedAt.Time
		}
	}
	days, err := settings.BusinessDates(ctx, env.Q, env.Caller.StoreID, times)
	if err != nil {
		return nil, err
	}
	out := &RedemptionList{Items: make([]Redemption, 0, len(rows))}
	for i, row := range rows {
		if days[i].Before(from) || days[i].After(to) {
			continue
		}
		if in.Status != "" && row.Status != in.Status {
			continue
		}
		if in.UnlinkedOnly && row.OrderID != nil {
			continue
		}
		out.Items = append(out.Items, *view(row))
	}
	return out, nil
}

func platformError(err error) error {
	if errors.Is(err, ErrUnknown) {
		return ops.Rule("结果未知，请稍后查询")
	}
	return ops.Rule("团购平台操作失败")
}

func redeem(ctx context.Context, env ops.Env, in CouponRequest, providers *Providers) (*Redemption, error) {
	in.TenantID = env.Caller.TenantID
	in.StoreID = env.Caller.StoreID
	var row dbgen.CouponRedemption
	var fresh bool
	err := env.Transact(ctx, func(q *dbgen.Queries) error {
		if err := q.LockCouponIdentity(ctx, dbgen.LockCouponIdentityParams{StoreID: env.Caller.StoreID, Platform: in.Platform, CouponCode: in.CouponCode}); err != nil {
			return err
		}
		active, err := q.HasOtherActiveRedemption(ctx, dbgen.HasOtherActiveRedemptionParams{StoreID: env.Caller.StoreID, Platform: in.Platform, CouponCode: in.CouponCode, IdempotencyKey: in.Key})
		if err != nil {
			return err
		}
		if active {
			return ops.Rule("这张券已有核销记录，请使用原幂等键查询或重试")
		}
		n, err := q.CreateRedemption(ctx, dbgen.CreateRedemptionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Platform: in.Platform, CouponCode: in.CouponCode, IdempotencyKey: in.Key, RedeemedBy: env.Caller.StaffID, CreatedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}})
		if err != nil {
			return err
		}
		fresh = n == 1
		row, err = q.LockRedemption(ctx, dbgen.LockRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: in.Key})
		if err != nil {
			return err
		}
		if row.Platform != in.Platform || row.CouponCode != in.CouponCode {
			return ops.Rule("幂等键已用于其他券")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if row.Status == "redeemed" {
		return recordExisting(ctx, env, row, "redeem_coupon")
	}
	if row.Status == "revoked" || row.Status == "failed" {
		return nil, ops.Rule("当前状态不可核销")
	}
	p := providers.Get(in.Platform)
	if p == nil {
		return nil, ops.Rule("团购平台未配置")
	}
	var result CouponResult
	if !fresh {
		result, err = p.Query(ctx, in)
		if err != nil {
			return finish(ctx, env, in.Key, "redeem_coupon", CouponResult{}, ErrUnknown)
		}
		if result.Status != "unused" || result.Platform != in.Platform {
			return finish(ctx, env, in.Key, "redeem_coupon", result, nil)
		}
	}
	result, err = p.Redeem(ctx, in)
	return finish(ctx, env, in.Key, "redeem_coupon", result, err)
}

func revoke(ctx context.Context, env ops.Env, in LookupInput, providers *Providers) (*Redemption, error) {
	var row dbgen.CouponRedemption
	err := env.Transact(ctx, func(q *dbgen.Queries) error {
		var err error
		row, err = q.LockRedemption(ctx, dbgen.LockRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: in.Key})
		if errors.Is(err, pgx.ErrNoRows) {
			return ops.ErrNotFound
		}
		if err != nil {
			return err
		}
		if row.Status == "revoked" {
			return nil
		}
		if row.Status != "redeemed" {
			return ops.Rule("只允许撤销已核销的券")
		}
		return linkedOrderRule(ctx, q, env.Caller.StoreID, row)
	})
	if err != nil {
		return nil, err
	}
	if row.Status == "revoked" {
		return recordExisting(ctx, env, row, "revoke_coupon")
	}
	p := providers.Get(row.Platform)
	if p == nil {
		return nil, ops.Rule("团购平台未配置")
	}
	request := CouponRequest{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Platform: row.Platform, CouponCode: row.CouponCode, Key: "revoke:" + in.Key}
	result, err := p.Query(ctx, request)
	if err != nil {
		return finish(ctx, env, in.Key, "revoke_coupon", CouponResult{}, ErrUnknown)
	}
	if result.Platform != row.Platform {
		return finish(ctx, env, in.Key, "revoke_coupon", CouponResult{}, ErrUnknown)
	}
	if result.Status == "redeemed" {
		if row.Platform == "douyin" && env.Now.Sub(row.RedeemedAt.Time) > time.Hour {
			return nil, ops.Rule("抖音核销超过一小时不可撤销")
		}
		result, err = p.Revoke(ctx, request)
	}
	return finish(ctx, env, in.Key, "revoke_coupon", result, err)
}

func recordExisting(ctx context.Context, env ops.Env, row dbgen.CouponRedemption, action string) (*Redemption, error) {
	var out *Redemption
	err := env.Transact(ctx, func(q *dbgen.Queries) error {
		current, err := q.LockRedemption(ctx, dbgen.LockRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: row.IdempotencyKey})
		if err != nil {
			return err
		}
		out = view(current)
		env.Q = q
		return audit.Record(ctx, env, action, audit.CouponRedemptionTarget(current.ID), out, out)
	})
	return out, err
}

// finish 在新事务中重读并锁住记录，晚到的响应不得倒退已完成的状态。
func finish(ctx context.Context, env ops.Env, key, action string, result CouponResult, platformErr error) (*Redemption, error) {
	var out *Redemption
	var outcome error
	err := env.Transact(ctx, func(q *dbgen.Queries) error {
		row, err := q.LockRedemption(ctx, dbgen.LockRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: key})
		if err != nil {
			return err
		}
		before := view(row)
		// 平台已经撤销时以平台结果为准；即使券在初次检查后被关联，也要落 revoked。
		if action == "revoke_coupon" && platformErr == nil && result.Platform == row.Platform && result.Status == "revoked" && row.Status == "redeemed" {
			orderStatus := ""
			if row.OrderID != nil {
				order, err := q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: env.Caller.StoreID, ID: *row.OrderID})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				if err == nil {
					orderStatus = order.Status
				}
			}
			if row.OrderID != nil && orderStatus == "open" {
				row, err = q.RevokeRedemption(ctx, dbgen.RevokeRedemptionParams{StoreID: env.Caller.StoreID, ID: row.ID, RevokedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}})
			} else {
				row, err = q.UpdateRedemption(ctx, dbgen.UpdateRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: key, Status: "revoked", RedeemedAt: row.RedeemedAt, RevokedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}})
				if err == nil && row.OrderID != nil && orderStatus == "paid" {
					outcome = ops.Rule("团购券已被平台撤销；该券已用于已结账的订单，请撤单或走售后处理")
				}
			}
			if err != nil {
				return err
			}
			out = view(row)
			env.Q = q
			return audit.Record(ctx, env, action, audit.CouponRedemptionTarget(row.ID), before, out)
		}
		mutable := row.Status == "pending" || row.Status == "unknown"
		if action == "revoke_coupon" {
			mutable = row.Status == "redeemed"
		}
		if mutable {
			if platformErr != nil {
				outcome = platformError(platformErr)
				if action == "redeem_coupon" {
					row.Status = "failed"
					if errors.Is(platformErr, ErrUnknown) {
						row.Status = "unknown"
					}
				}
			} else if result.Platform != row.Platform || (result.Status == "redeemed" && (result.RedeemedAt == nil || result.RedeemedAt.IsZero())) {
				outcome = platformError(ErrUnknown)
				if action == "redeem_coupon" {
					row.Status = "unknown"
				}
			} else if action == "redeem_coupon" && result.Status == "redeemed" {
				row.Status = "redeemed"
				row.RedeemedAt = pgtype.Timestamptz{Time: *result.RedeemedAt, Valid: true}
			} else if result.Status == "revoked" {
				row.Status = "revoked"
				row.RevokedAt = pgtype.Timestamptz{Time: env.Now, Valid: true}
			} else {
				outcome = ops.Rule("平台券状态不允许此操作")
				if action == "redeem_coupon" {
					row.Status = "failed"
				}
			}
			row, err = q.UpdateRedemption(ctx, dbgen.UpdateRedemptionParams{StoreID: env.Caller.StoreID, IdempotencyKey: key, Status: row.Status, RedeemedAt: row.RedeemedAt, RevokedAt: row.RevokedAt})
			if err != nil {
				return err
			}
		}
		out = view(row)
		env.Q = q
		if outcome != nil {
			return audit.Record(ctx, env, action, audit.CouponRedemptionTarget(row.ID), before, map[string]any{"redemption": out, "error": outcome.Error()})
		}
		return audit.Record(ctx, env, action, audit.CouponRedemptionTarget(row.ID), before, out)
	})
	if err != nil {
		return nil, err
	}
	return out, outcome
}
