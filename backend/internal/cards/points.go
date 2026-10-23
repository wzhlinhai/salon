package cards

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// PointsLedgerEntry 是积分的一次变动。
type PointsLedgerEntry struct {
	ID           uuid.UUID       `json:"id"`
	Kind         string          `json:"kind"`
	Before       int32           `json:"before"`
	Delta        int32           `json:"delta"`
	After        int32           `json:"after"`
	Reason       string          `json:"reason,omitempty"`
	BusinessDate string          `json:"business_date"`
	At           time.Time       `json:"at"`
	Actor        CardLedgerActor `json:"actor"`
}

// ListCardPointsInput 是积分流水的游标分页参数。
type ListCardPointsInput struct {
	CardID   uuid.UUID  `json:"card_id"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// PointsLedgerList 是从新到旧的积分流水。
type PointsLedgerList struct {
	Entries []PointsLedgerEntry `json:"entries"`
}

func earnPoints(ctx context.Context, env ops.Env, ledger dbgen.CardLedger, principal money.Amount) error {
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, ledger.BusinessDate.Time)
	if err != nil {
		return err
	}
	rate := rules.PointsPerRechargeYuan()
	if rate == 0 {
		return nil
	}
	earned := principal.WholeDiv(int64(rate))
	if earned < 1 {
		return nil
	}
	current, err := cardView(ctx, env, ledger.CardID)
	if err != nil {
		return err
	}
	before := current.Points
	return writePoints(ctx, env, dbgen.AddCardPointsParams{CardID: ledger.CardID, Kind: ledger.Kind, CardLedgerID: &ledger.ID, BusinessDate: ledger.BusinessDate}, before, before+int32(earned))
}

func fillPoints(ctx context.Context, env ops.Env, cards []Card) error {
	ids := make([]uuid.UUID, len(cards))
	index := map[uuid.UUID]int{}
	for i := range cards {
		ids[i] = cards[i].ID
		index[cards[i].ID] = i
	}
	rows, err := env.Q.CardPoints(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range rows {
		cards[index[r.CardID]].Points = r.After
	}
	return nil
}

func listCardPoints(ctx context.Context, env ops.Env, in *ListCardPointsInput) (*PointsLedgerList, error) {
	card, err := env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := canView(ctx, env, card.CustomerID); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListCardPoints(ctx, dbgen.ListCardPointsParams{CardID: card.ID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := &PointsLedgerList{Entries: []PointsLedgerEntry{}}
	for _, r := range rows {
		l := r.CardPointsLedger
		out.Entries = append(out.Entries, PointsLedgerEntry{ID: l.ID, Kind: l.Kind, Before: l.Before, Delta: l.Delta, After: l.After, Reason: r.Reason, BusinessDate: l.BusinessDate.Time.Format(time.DateOnly), At: l.At.Time, Actor: CardLedgerActor{ID: l.ActorID, Name: r.ActorName}})
	}
	return out, nil
}

func adjustPoints(ctx context.Context, env ops.Env, cardID, adjustmentID uuid.UUID, before, after int32, day time.Time) error {
	return writePoints(ctx, env, dbgen.AddCardPointsParams{CardID: cardID, Kind: "adjust", AdjustmentID: &adjustmentID, BusinessDate: pgtype.Date{Time: day, Valid: true}}, before, after)
}
