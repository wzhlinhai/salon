// Package salepayments 记录开卡、充值、补差价和卖红包时顾客实际支付的收款方式。
package salepayments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/paymethod"
)

// Payment 是一笔销售收款的方式和金额。
type Payment struct {
	Method string       `json:"method" enum:"cash,unionpay,wechat,alipay"`
	Amount money.Amount `json:"amount"`
}

// Validate 检查方式、金额和合计；金额格式错误返回 422，合计不一致返回 409。
func Validate(payments []Payment, expected money.Total) error {
	var total money.Total
	for _, p := range payments {
		valid := false
		for _, m := range paymethod.OnSite() {
			if p.Method == string(m.Code) {
				valid = true
				break
			}
		}
		if !valid {
			return &ops.ToolError{Messages: []string{"收款方式只能是现金、银联、微信或支付宝"}}
		}
		if p.Amount.Cmp(money.Amount{}) <= 0 {
			return &ops.ToolError{Messages: []string{"收款金额必须大于零"}}
		}
		total = total.AddProduct(p.Amount, 1)
	}
	if len(payments) > 0 && total.CmpTotal(expected) != 0 {
		return ops.Rule("收款合计必须等于 " + expected.Format())
	}
	if len(payments) == 0 && expected.CmpTotal(money.Total{}) != 0 {
		return ops.Rule("收款合计必须等于 " + expected.Format())
	}
	return nil
}

// WriteCard 把一次开卡、充值或补差价的实际收款追加到卡流水。
func WriteCard(ctx context.Context, env ops.Env, ledger dbgen.CardLedger, payments []Payment) error {
	return write(ctx, env, payments, &ledger.ID, nil, ledger.BusinessDate)
}

// WriteRedPacket 把一次卖红包的实际收款追加到红包。
func WriteRedPacket(ctx context.Context, env ops.Env, packet dbgen.RedPacket, payments []Payment) error {
	return write(ctx, env, payments, nil, &packet.ID, packet.BusinessDate)
}

func write(ctx context.Context, env ops.Env, payments []Payment, ledgerID, packetID *uuid.UUID, day pgtype.Date) error {
	for _, p := range payments {
		if err := env.Q.AddSalePayment(ctx, dbgen.AddSalePaymentParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			CardLedgerID: ledgerID, RedPacketID: packetID,
			Method: p.Method, Amount: p.Amount, BusinessDate: day, CreatedBy: env.Caller.StaffID,
		}); err != nil {
			return err
		}
	}
	return nil
}
