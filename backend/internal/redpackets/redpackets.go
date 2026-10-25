package redpackets

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明红包发放、撤回及列表操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[IssueRedPacketInput, RedPacket]{Name: "issue_red_packet", Summary: "卖或送红包给顾客", Kind: ops.Write, Permission: perm.CardSell, AI: ops.AIDraft, Handle: issueRedPacket})
	ops.Register(r, ops.Operation[RevokeRedPacketInput, RedPacket]{Name: "revoke_red_packet", Summary: "撤回未用的红包", Kind: ops.Write, Permission: perm.CardAdjust, AI: ops.AIDraft, Handle: revokeRedPacket})
	ops.Register(r, ops.Operation[ListCustomerRedPacketsInput, RedPacketList]{Name: "list_customer_red_packets", Summary: "查看顾客红包", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCustomerRedPackets})
}

func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}

func issueRedPacket(ctx context.Context, env ops.Env, in *IssueRedPacketInput) (*RedPacket, error) {
	return issueRedPacketAt(ctx, env, in, time.Now())
}

func issueRedPacketAt(ctx context.Context, env ops.Env, in *IssueRedPacketInput, now time.Time) (*RedPacket, error) {
	if in.FaceValue.Cmp(money.Amount{}) <= 0 {
		return nil, ops.Rule("面额须大于零")
	}
	switch in.Kind {
	case "sold":
		if in.Price == nil || in.Price.Cmp(money.Amount{}) <= 0 {
			return nil, ops.Rule("卖红包须填写大于零的售价")
		}
	case "gift":
		if in.Price != nil || len(in.SalesStaffIDs) > 0 {
			return nil, ops.Rule("送红包不能填写售价或业绩归属人")
		}
	default:
		return nil, ops.Rule("红包种类无效")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	expires, err := time.Parse(time.DateOnly, in.ExpiresOn)
	if err != nil {
		return nil, ops.Rule("有效期无效")
	}
	if expires.Format(time.DateOnly) < day.Format(time.DateOnly) {
		return nil, ops.Rule("有效期不能早于今天")
	}
	// 发放先共享锁顾客再共享锁员工；不在持有红包锁后回锁顾客。
	if _, err := customers.LockReference(ctx, env, in.CustomerID); err != nil {
		return nil, rowError(err)
	}
	if in.Kind == "sold" {
		if err := staff.LockSalesStaff(ctx, env, in.SalesStaffIDs); err != nil {
			return nil, err
		}
	}
	row, err := env.Q.CreateRedPacket(ctx, dbgen.CreateRedPacketParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: in.CustomerID, Kind: in.Kind, FaceValue: in.FaceValue, Price: in.Price, ExpiresOn: pgtype.Date{Time: expires, Valid: true}, BusinessDate: pgtype.Date{Time: day, Valid: true}, IssuedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	for _, id := range in.SalesStaffIDs {
		if err := env.Q.AddRedPacketSales(ctx, dbgen.AddRedPacketSalesParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, RedPacketID: row.ID, StaffID: id}); err != nil {
			return nil, err
		}
	}
	out, err := view(ctx, env, row, day)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "issue_red_packet", audit.CustomerTarget(row.CustomerID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func revokeRedPacket(ctx context.Context, env ops.Env, in *RevokeRedPacketInput) (*RedPacket, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return nil, ops.Rule("请填写不超过200字的撤回原因")
	}
	row, err := env.Q.LockRedPacket(ctx, dbgen.LockRedPacketParams{StoreID: env.Caller.StoreID, ID: in.RedPacketID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := customers.CheckViewable(ctx, env, row.CustomerID); err != nil {
		return nil, err
	}
	if row.OrderPaymentID != nil {
		return nil, ops.Rule("红包已用，不能撤回")
	}
	if row.RevokedAt.Valid {
		return nil, ops.Rule("红包已撤回")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	before, err := view(ctx, env, row, day)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.RevokeRedPacket(ctx, dbgen.RevokeRedPacketParams{ID: row.ID, RevokedBy: &env.Caller.StaffID, RevokeReason: &reason})
	if err != nil {
		return nil, err
	}
	out, err := view(ctx, env, row, day)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "revoke_red_packet", audit.CustomerTarget(row.CustomerID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func listCustomerRedPackets(ctx context.Context, env ops.Env, in *ListCustomerRedPacketsInput) (*RedPacketList, error) {
	if err := customers.CheckViewable(ctx, env, in.CustomerID); err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCustomerRedPackets(ctx, dbgen.ListCustomerRedPacketsParams{StoreID: env.Caller.StoreID, CustomerID: in.CustomerID})
	if err != nil {
		return nil, err
	}
	items, err := views(ctx, env, rows, day)
	if err != nil {
		return nil, err
	}
	return &RedPacketList{Items: items}, nil
}

func view(ctx context.Context, env ops.Env, row dbgen.RedPacket, day time.Time) (*RedPacket, error) {
	items, err := views(ctx, env, []dbgen.RedPacket{row}, day)
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func views(ctx context.Context, env ops.Env, rows []dbgen.RedPacket, day time.Time) ([]RedPacket, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	sales, err := env.Q.ListRedPacketSales(ctx, dbgen.ListRedPacketSalesParams{StoreID: env.Caller.StoreID, RedPacketIds: ids})
	if err != nil {
		return nil, err
	}
	bySales := map[uuid.UUID][]RedPacketSalesStaff{}
	for _, s := range sales {
		bySales[s.RedPacketID] = append(bySales[s.RedPacketID], RedPacketSalesStaff{ID: s.ID, Name: s.Name})
	}
	payments, err := env.Q.ListRedPacketPayments(ctx, dbgen.ListRedPacketPaymentsParams{StoreID: env.Caller.StoreID, RedPacketIds: ids})
	if err != nil {
		return nil, err
	}
	byPayment := map[uuid.UUID]dbgen.ListRedPacketPaymentsRow{}
	for _, p := range payments {
		byPayment[p.RedPacketID] = p
	}
	items := make([]RedPacket, 0, len(rows))
	for _, row := range rows {
		staff := bySales[row.ID]
		if staff == nil {
			staff = []RedPacketSalesStaff{}
		}
		out := RedPacket{ID: row.ID, Kind: row.Kind, FaceValue: row.FaceValue, Price: row.Price, SalesStaff: staff, ExpiresOn: row.ExpiresOn.Time.Format(time.DateOnly), BusinessDate: row.BusinessDate.Time.Format(time.DateOnly), IssuedBy: row.IssuedBy, At: row.At.Time, Status: "available", RevokeReason: row.RevokeReason, RevokedBy: row.RevokedBy}
		if row.OrderPaymentID != nil {
			p := byPayment[row.ID]
			out.Status = "used"
			out.OrderID = &p.OrderID
			out.UsedAmount = &p.Amount
		} else if row.RevokedAt.Valid {
			out.Status = "revoked"
			out.RevokedAt = &row.RevokedAt.Time
		} else if row.ExpiresOn.Time.Format(time.DateOnly) < day.Format(time.DateOnly) {
			out.Status = "expired"
		}
		items = append(items, out)
	}
	return items, nil
}

// LockPayments 在调用者门店按 UUID 一起锁住本次结账的全部红包。
func LockPayments(ctx context.Context, env ops.Env, ids []uuid.UUID) (map[uuid.UUID]dbgen.RedPacket, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out := map[uuid.UUID]dbgen.RedPacket{}
	for _, id := range slices.Compact(ordered) {
		row, err := env.Q.LockRedPacket(ctx, dbgen.LockRedPacketParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return nil, rowError(err)
		}
		out[id] = row
	}
	return out, nil
}

// CheckUsable 在持锁后检查红包归属、查看范围和营业日可用状态。
func CheckUsable(ctx context.Context, env ops.Env, row dbgen.RedPacket, customerID uuid.UUID, day time.Time) error {
	if err := customers.CheckViewable(ctx, env, row.CustomerID); err != nil {
		return err
	}
	if row.CustomerID != customerID {
		return ops.Rule("只能用这位顾客自己的红包")
	}
	if row.OrderPaymentID != nil {
		return ops.Rule("红包已用")
	}
	if row.RevokedAt.Valid {
		return ops.Rule("红包已撤回")
	}
	if row.ExpiresOn.Time.Format(time.DateOnly) < day.Format(time.DateOnly) {
		return ops.Rule("红包已过期")
	}
	return nil
}

// MarkUsed 在持锁的调用者事务中把红包一次用掉并关联支付。
func MarkUsed(ctx context.Context, env ops.Env, id, paymentID uuid.UUID) error {
	return env.Q.UseRedPacket(ctx, dbgen.UseRedPacketParams{ID: id, OrderPaymentID: &paymentID})
}

// Restore 在已持红包锁的事务中退回支付并记录改前改后日志。
func Restore(ctx context.Context, env ops.Env, row dbgen.RedPacket, day time.Time) error {
	before, err := view(ctx, env, row, day)
	if err != nil {
		return err
	}
	if err := env.Q.UseRedPacket(ctx, dbgen.UseRedPacketParams{ID: row.ID}); err != nil {
		return err
	}
	row.OrderPaymentID = nil
	after, err := view(ctx, env, row, day)
	if err != nil {
		return err
	}
	return audit.Record(ctx, env, "void_order", audit.CustomerTarget(row.CustomerID), before, after)
}
