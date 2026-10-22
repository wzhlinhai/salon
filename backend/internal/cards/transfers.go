package cards

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// TransferCardBalanceInput 是两张卡之间转余额的金额及原因。
type TransferCardBalanceInput struct {
	FromCardID uuid.UUID    `json:"from_card_id"`
	ToCardID   uuid.UUID    `json:"to_card_id"`
	Principal  money.Amount `json:"principal"`
	Bonus      money.Amount `json:"bonus"`
	Reason     string       `json:"reason" minLength:"1" maxLength:"200"`
}

// CardTransferResult 是转余额后的两张卡。
type CardTransferResult struct {
	From Card `json:"from"`
	To   Card `json:"to"`
}

func registerTransfers(r *ops.Registry) {
	ops.Register(r, ops.Operation[TransferCardBalanceInput, CardTransferResult]{Name: "transfer_card_balance", Summary: "在两张卡之间转余额并记录原因", Kind: ops.Write, Permission: perm.CardAdjust, AI: ops.AIDraft, Handle: transferCardBalance})
}
func transferCardBalance(ctx context.Context, env ops.Env, in *TransferCardBalanceInput) (*CardTransferResult, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 200 {
		return nil, ops.Rule("原因须为一到二百字")
	}
	if in.FromCardID == in.ToCardID {
		return nil, ops.Rule("不能向同一卡转余额")
	}
	if in.Principal.Cmp(money.Amount{}) == 0 && in.Bonus.Cmp(money.Amount{}) == 0 {
		return nil, ops.Rule("转余额合计须大于零")
	}
	ids := []uuid.UUID{in.FromCardID, in.ToCardID}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	cards := map[uuid.UUID]dbgen.Card{}
	for _, id := range ids {
		card, err := env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return nil, rowError(err)
		}
		cards[id] = card
	}
	if err := canView(ctx, env, cards[in.FromCardID].CustomerID); err != nil {
		return nil, err
	}
	if err := canView(ctx, env, cards[in.ToCardID].CustomerID); err != nil {
		return nil, err
	}
	from, err := cardView(ctx, env, in.FromCardID)
	if err != nil {
		return nil, err
	}
	to, err := cardView(ctx, env, in.ToCardID)
	if err != nil {
		return nil, err
	}
	if to.Kind == CountCard {
		return nil, ops.Rule("次卡不能接收余额")
	}
	if from.Principal.Cmp(in.Principal) < 0 || from.Bonus.Cmp(in.Bonus) < 0 {
		return nil, ops.Rule("余额不足")
	}
	if _, err := to.Principal.Add(in.Principal); err != nil {
		return nil, ops.Rule("卡余额超过上限")
	}
	if _, err := to.Bonus.Add(in.Bonus); err != nil {
		return nil, ops.Rule("卡余额超过上限")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	date := pgtype.Date{Time: day, Valid: true}
	outLedger, err := env.Q.AddCardTransferLedger(ctx, dbgen.AddCardTransferLedgerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: from.ID, Kind: "transfer_out", PrincipalBefore: from.Principal, PrincipalDelta: money.Amount{}.Sub(in.Principal), BonusBefore: from.Bonus, BonusDelta: money.Amount{}.Sub(in.Bonus), BusinessDate: date, ActorID: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	inLedger, err := env.Q.AddCardTransferLedger(ctx, dbgen.AddCardTransferLedgerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: to.ID, Kind: "transfer_in", PrincipalBefore: to.Principal, PrincipalDelta: in.Principal.Sub(money.Amount{}), BonusBefore: to.Bonus, BonusDelta: in.Bonus.Sub(money.Amount{}), BusinessDate: date, ActorID: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	if err := env.Q.AddCardTransfer(ctx, dbgen.AddCardTransferParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, FromCardID: from.ID, ToCardID: to.ID, Reason: in.Reason, FromLedgerID: outLedger.ID, ToLedgerID: inLedger.ID, BusinessDate: date, ActorID: env.Caller.StaffID}); err != nil {
		return nil, err
	}
	afterFrom, err := cardView(ctx, env, from.ID)
	if err != nil {
		return nil, err
	}
	afterTo, err := cardView(ctx, env, to.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "transfer_card_balance", audit.CardTarget(from.ID, cards[from.ID].CustomerID), from, afterFrom); err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "transfer_card_balance", audit.CardTarget(to.ID, cards[to.ID].CustomerID), to, afterTo); err != nil {
		return nil, err
	}
	return &CardTransferResult{From: *afterFrom, To: *afterTo}, nil
}
