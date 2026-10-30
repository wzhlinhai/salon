package cards

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
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
	out, err := change(ctx, env, "transfer_card_balance", []uuid.UUID{in.FromCardID, in.ToCardID}, func(changes []cardChange, day time.Time) error {
		from, to := changes[0].before, changes[1].before
		if to.Kind == CountCard {
			return ops.Rule("次卡不能接收余额")
		}
		if from.Principal.Cmp(in.Principal) < 0 || from.Bonus.Cmp(in.Bonus) < 0 {
			return ops.Rule("余额不足")
		}
		date := pgtype.Date{Time: day, Valid: true}
		outLedger, err := writeBalance(ctx, env, from.ID, "transfer_out", from.Principal, from.Bonus, money.Amount{}.Sub(in.Principal), money.Amount{}.Sub(in.Bonus), day, nil, nil, nil)
		if err != nil {
			return err
		}
		inLedger, err := writeBalance(ctx, env, to.ID, "transfer_in", to.Principal, to.Bonus, in.Principal.Sub(money.Amount{}), in.Bonus.Sub(money.Amount{}), day, nil, nil, nil)
		if err != nil {
			return err
		}
		if err := env.Q.AddCardTransfer(ctx, dbgen.AddCardTransferParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, FromCardID: from.ID, ToCardID: to.ID, Reason: in.Reason, FromLedgerID: outLedger.ID, ToLedgerID: inLedger.ID, BusinessDate: date, ActorID: env.Caller.StaffID}); err != nil {
			return err
		}
		return nil
	}, time.Now())
	if err != nil {
		return nil, err
	}
	return &CardTransferResult{From: *out[0], To: *out[1]}, nil
}
