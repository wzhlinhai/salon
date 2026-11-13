package aftersales

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// refundState 是原单当前可退金额和已审批退款占用。
type refundState struct {
	base         money.Total
	baseFen      int64
	approved     money.Total
	remaining    money.Total
	cardPaid     map[uuid.UUID]money.Total
	cardRefunded map[uuid.UUID]money.Total
}

// cardBalance 是乐观锁卡后读取的余额和状态。
type cardBalance struct {
	principal money.Amount
	bonus     money.Amount
	status    string
}

func parseAmount(s string) (money.Amount, error) {
	var a money.Amount
	err := a.Scan(s)
	return a, err
}

func loadRefundState(ctx context.Context, env ops.Env, orderID uuid.UUID) (*refundState, error) {
	baseText, err := env.Q.OrderRefundableBase(ctx, dbgen.OrderRefundableBaseParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return nil, err
	}
	baseAmount, err := parseAmount(baseText)
	if err != nil {
		return nil, err
	}
	state := &refundState{
		base:         (money.Total{}).AddProduct(baseAmount, 1),
		cardPaid:     map[uuid.UUID]money.Total{},
		cardRefunded: map[uuid.UUID]money.Total{},
	}
	state.baseFen = state.base.Fen()
	approved, err := env.Q.ApprovedRefundsByMethod(ctx, dbgen.ApprovedRefundsByMethodParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return nil, err
	}
	for _, row := range approved {
		amount, err := parseAmount(row.Amount)
		if err != nil {
			return nil, err
		}
		state.approved = state.approved.AddProduct(amount, 1)
	}
	paid, err := env.Q.OrderCardPrincipalPaid(ctx, dbgen.OrderCardPrincipalPaidParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return nil, err
	}
	for _, row := range paid {
		if row.CardID == nil {
			continue
		}
		amount, err := parseAmount(row.Amount)
		if err != nil {
			return nil, err
		}
		state.cardPaid[*row.CardID] = state.cardPaid[*row.CardID].AddProduct(amount, 1)
	}
	cardRefunds, err := env.Q.ApprovedCardPrincipalRefunds(ctx, dbgen.ApprovedCardPrincipalRefundsParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return nil, err
	}
	for _, row := range cardRefunds {
		if row.CardID == nil {
			continue
		}
		amount, err := parseAmount(row.Amount)
		if err != nil {
			return nil, err
		}
		state.cardRefunded[*row.CardID] = state.cardRefunded[*row.CardID].AddProduct(amount, 1)
	}
	if state.base.CmpTotal(state.approved) < 0 {
		return nil, errors.New("aftersales: approved refunds exceed refundable base")
	}
	state.remaining = state.base.DeductTotal(state.approved)
	return state, nil
}

// validateRefundLines 校验退款方式和金额，返回本次退款合计。
func validateRefundLines(typ string, lines []RefundLine, state *refundState) (money.Total, error) {
	if typ == "repair" || typ == "redo" {
		if len(lines) != 0 {
			return money.Total{}, ops.Rule("修补、重做不能填写退款")
		}
		return money.Total{}, nil
	}
	if typ == "partial_refund" && len(lines) == 0 {
		return money.Total{}, ops.Rule("部分退款至少填写一笔退款")
	}
	if typ == "full_refund" && state.remaining.CmpTotal(money.Total{}) == 0 {
		return money.Total{}, ops.Rule("没有可退金额")
	}
	var total money.Total
	perCard := map[uuid.UUID]money.Total{}
	for _, line := range lines {
		switch line.Method {
		case "cash", "unionpay", "wechat", "alipay":
			if line.CardID != nil {
				return money.Total{}, &ops.ToolError{Messages: []string{"只有卡本金退款才能指定卡"}}
			}
		case "card_principal":
			if line.CardID == nil {
				return money.Total{}, &ops.ToolError{Messages: []string{"卡本金退款必须指定卡"}}
			}
			if state.cardPaid[*line.CardID].CmpTotal(money.Total{}) == 0 {
				return money.Total{}, ops.Rule("卡本金退款只能退回原单支付过本金的卡")
			}
			perCard[*line.CardID] = perCard[*line.CardID].AddProduct(line.Amount, 1)
		default:
			return money.Total{}, &ops.ToolError{Messages: []string{"退款方式只能是现金、银联、微信、支付宝或卡本金"}}
		}
		if line.Amount.Cmp(money.Amount{}) <= 0 {
			return money.Total{}, &ops.ToolError{Messages: []string{"退款金额必须大于零"}}
		}
		total = total.AddProduct(line.Amount, 1)
	}
	if typ == "full_refund" && total.CmpTotal(state.remaining) != 0 {
		return money.Total{}, ops.Rule("全额退款合计必须等于可退金额 " + state.remaining.Format())
	}
	if total.CmpTotal(state.remaining) > 0 {
		return money.Total{}, ops.Rule("退款合计不能超过可退金额 " + state.remaining.Format())
	}
	for cardID, amount := range perCard {
		used := state.cardRefunded[cardID].AddTotal(amount)
		if used.CmpTotal(state.cardPaid[cardID]) > 0 {
			return money.Total{}, ops.Rule("卡本金退款超过这张卡原单支付的本金")
		}
	}
	return total, nil
}

func cardIDs(lines []RefundLine) []uuid.UUID {
	ids := []uuid.UUID{}
	for _, line := range lines {
		if line.Method == "card_principal" && line.CardID != nil {
			ids = append(ids, *line.CardID)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(ids)
}

// checkCardsNotRefunded 在未持卡锁的创建路径检查卡状态。
func checkCardsNotRefunded(ctx context.Context, env ops.Env, lines []RefundLine) error {
	for _, id := range cardIDs(lines) {
		row, err := env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ops.ErrNotFound
		}
		if err != nil {
			return err
		}
		if row.Status == "refunded" {
			return ops.Rule("卡已退，不能退回本金")
		}
	}
	return nil
}

// lockCards 按 UUID 顺序锁卡并读取余额，供审批时重算和退款入账。
func lockCards(ctx context.Context, env ops.Env, lines []RefundLine) (map[uuid.UUID]cardBalance, error) {
	out := map[uuid.UUID]cardBalance{}
	for _, id := range cardIDs(lines) {
		row, err := env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		view, err := env.Q.CardView(ctx, dbgen.CardViewParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return nil, err
		}
		out[id] = cardBalance{principal: view.PrincipalAfter, bonus: view.BonusAfter, status: row.Status}
	}
	for _, card := range out {
		if card.status == "refunded" {
			return nil, ops.Rule("卡已退，不能退回本金")
		}
	}
	return out, nil
}
