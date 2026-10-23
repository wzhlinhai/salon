package orders

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func registerCheckout(r *ops.Registry) {
	ops.Register(r, ops.Operation[CheckoutOrdersInput, OrderList]{Name: "checkout_orders", Summary: "一张或几张挂单一起结账并登记支付", Kind: ops.Write, Permission: perm.PaymentCollect, AI: ops.AIDraft, Handle: checkoutOrders})
}

func checkoutOrders(ctx context.Context, env ops.Env, in *CheckoutOrdersInput) (*OrderList, error) {
	return checkoutOrdersAt(ctx, env, in, time.Now())
}

func checkoutOrdersAt(ctx context.Context, env ops.Env, in *CheckoutOrdersInput, now time.Time) (*OrderList, error) {
	ids := make([]uuid.UUID, len(in.Orders))
	seen := map[uuid.UUID]bool{}
	for i, order := range in.Orders {
		if seen[order.OrderID] {
			return nil, ops.Rule("同一张单不能重复结账")
		}
		seen[order.OrderID] = true
		ids[i] = order.OrderID
	}
	after, err := change(ctx, env, "checkout_orders", ids, func(rows []dbgen.Order) error {
		orders, err := views(ctx, env, rows)
		if err != nil {
			return err
		}
		for i, order := range orders {
			if err := checkCheckout(order, in.Orders[i].Payments); err != nil {
				return err
			}
		}
		day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		checkout, err := env.Q.CreateCheckout(ctx, dbgen.CreateCheckoutParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BusinessDate: pgtype.Date{Time: day, Valid: true}, CheckedOutBy: env.Caller.StaffID})
		if err != nil {
			return err
		}
		for i := range rows {
			for _, payment := range in.Orders[i].Payments {
				if err := env.Q.CreateOrderPayment(ctx, dbgen.CreateOrderPaymentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CheckoutID: checkout.ID, OrderID: rows[i].ID, Method: payment.Method, Amount: payment.Amount}); err != nil {
					return err
				}
			}
			if err := env.Q.MarkOrderPaid(ctx, dbgen.MarkOrderPaidParams{StoreID: env.Caller.StoreID, ID: rows[i].ID, CheckoutID: &checkout.ID}); err != nil {
				return err
			}
			rows[i].Status = "paid"
			rows[i].CheckoutID = &checkout.ID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &OrderList{Items: after}, nil
}

func checkCheckout(order Order, payments []PaymentInput) error {
	active := 0
	receivable := money.Total{}
	missing := false
	for _, item := range order.Items {
		if item.RemovedAt != nil {
			continue
		}
		active++
		receivable = receivable.AddProduct(item.Price, 1)
		for _, p := range item.Positions {
			if p.Required && p.StaffID == nil {
				missing = true
			}
		}
	}
	if active == 0 {
		return ops.Rule("单上没有项目")
	}
	if missing {
		return ops.Rule("必填岗位还没填人")
	}
	total := money.Total{}
	for _, payment := range payments {
		if payment.Amount.Cmp(money.Amount{}) <= 0 {
			return ops.Rule("支付金额须大于零")
		}
		total = total.AddProduct(payment.Amount, 1)
	}
	if total.CmpTotal(receivable) != 0 {
		return ops.Rule("支付合计须等于应收")
	}
	return nil
}
