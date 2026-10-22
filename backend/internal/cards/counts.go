package cards

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func typeItems(ctx context.Context, env ops.Env, types []CardType) error {
	ids := make([]uuid.UUID, len(types))
	for i := range types {
		ids[i] = types[i].ID
		types[i].Items = []CardTypeItem{}
	}
	rows, err := env.Q.ListCardTypeItems(ctx, ids)
	if err != nil {
		return err
	}
	byID := map[uuid.UUID][]CardTypeItem{}
	for _, r := range rows {
		x := r.CardTypeItem
		byID[x.CardTypeID] = append(byID[x.CardTypeID], CardTypeItem{CardTypeItemInput: CardTypeItemInput{ItemID: x.ItemID, PaidCount: x.PaidCount, GiftCount: x.GiftCount, UnitPrice: x.UnitPrice}, ItemName: r.ItemName})
	}
	for i := range types {
		if rows := byID[types[i].ID]; rows != nil {
			types[i].Items = rows
		}
	}
	return nil
}
func shareItem(ctx context.Context, env ops.Env, id uuid.UUID) error {
	item, err := env.Q.ShareCatalogItem(ctx, dbgen.ShareCatalogItemParams{StoreID: env.Caller.StoreID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !item.Active) {
		return ops.Rule("项目必须是本店启用的项目")
	}
	return err
}
func sameTypeItem(a, b CardTypeItemInput) bool {
	return a.ItemID == b.ItemID && a.PaidCount == b.PaidCount && a.GiftCount == b.GiftCount && a.UnitPrice.Cmp(b.UnitPrice) == 0
}
func changeTypeItems(ctx context.Context, env ops.Env, id uuid.UUID, previous []CardTypeItem, in []CardTypeItemInput) error {
	old := map[uuid.UUID]CardTypeItemInput{}
	for _, x := range previous {
		old[x.ItemID] = x.CardTypeItemInput
	}
	ordered := slices.Clone(in)
	slices.SortFunc(ordered, func(a, b CardTypeItemInput) int { return strings.Compare(a.ItemID.String(), b.ItemID.String()) })
	// 类型编辑只修改定义；已开卡的项目和单次金额保留各自快照。
	for _, x := range ordered {
		before, exists := old[x.ItemID]
		if exists && sameTypeItem(before, x) {
			delete(old, x.ItemID)
			continue
		}
		if err := shareItem(ctx, env, x.ItemID); err != nil {
			return err
		}
		if exists {
			if err := env.Q.UpdateCardTypeItem(ctx, dbgen.UpdateCardTypeItemParams{CardTypeID: id, ItemID: x.ItemID, PaidCount: x.PaidCount, GiftCount: x.GiftCount, UnitPrice: x.UnitPrice}); err != nil {
				return err
			}
		} else {
			if err := env.Q.AddCardTypeItem(ctx, dbgen.AddCardTypeItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardTypeID: id, ItemID: x.ItemID, PaidCount: x.PaidCount, GiftCount: x.GiftCount, UnitPrice: x.UnitPrice}); err != nil {
				return err
			}
		}
		delete(old, x.ItemID)
	}
	for _, x := range previous {
		if _, ok := old[x.ItemID]; ok {
			if err := env.Q.DeleteCardTypeItem(ctx, dbgen.DeleteCardTypeItemParams{CardTypeID: id, ItemID: x.ItemID}); err != nil {
				return err
			}
		}
	}
	return nil
}
func fillCounts(ctx context.Context, env ops.Env, cards []Card) error {
	ids := make([]uuid.UUID, len(cards))
	index := map[uuid.UUID]int{}
	prices := make([]decimal.Decimal, len(cards))
	for i := range cards {
		ids[i] = cards[i].ID
		index[cards[i].ID] = i
		cards[i].Items = []RemainingCounts{}
	}
	rows, err := env.Q.RemainingCounts(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range rows {
		x := r.CardItem
		i := index[x.CardID]
		cards[i].Items = append(cards[i].Items, RemainingCounts{ItemID: x.ItemID, ItemName: r.ItemName, UnitPrice: x.UnitPrice, PaidRemaining: r.PaidAfter, GiftRemaining: r.GiftAfter})
		price, err := decimal.NewFromString(x.UnitPrice.String())
		if err != nil {
			return err
		}
		prices[i] = prices[i].Add(price.Mul(decimal.NewFromInt(int64(x.PaidCountOpened))))
	}
	for i := range cards {
		cards[i].CountsPrice = prices[i].StringFixed(2)
	}
	return nil
}
func openCounts(ctx context.Context, env ops.Env, card uuid.UUID, ct uuid.UUID, day time.Time) error {
	rows, err := env.Q.ListCardTypeItems(ctx, []uuid.UUID{ct})
	if err != nil {
		return err
	}
	for _, r := range rows {
		x := r.CardTypeItem
		if err := shareItem(ctx, env, x.ItemID); err != nil {
			return err
		}
		if err := env.Q.AddCardItem(ctx, dbgen.AddCardItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card, ItemID: x.ItemID, UnitPrice: x.UnitPrice, PaidCountOpened: x.PaidCount, GiftCountOpened: x.GiftCount}); err != nil {
			return err
		}
		if err := env.Q.AddCardCountEntry(ctx, dbgen.AddCardCountEntryParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card, ItemID: x.ItemID, Kind: "open", PaidDelta: x.PaidCount, PaidAfter: x.PaidCount, GiftDelta: x.GiftCount, GiftAfter: x.GiftCount, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID}); err != nil {
			return err
		}
	}
	return nil
}
func countEntry(l dbgen.CardCountLedger, name, actor, reason string) CardCountEntry {
	return CardCountEntry{ID: l.ID, Item: CardCountItem{ID: l.ItemID, Name: name}, Kind: l.Kind, PaidBefore: l.PaidBefore, PaidDelta: l.PaidDelta, PaidAfter: l.PaidAfter, GiftBefore: l.GiftBefore, GiftDelta: l.GiftDelta, GiftAfter: l.GiftAfter, Reason: reason, BusinessDate: l.BusinessDate.Time.Format(time.DateOnly), At: l.At.Time, Actor: CardLedgerActor{ID: l.ActorID, Name: actor}}
}
func listCardCounts(ctx context.Context, env ops.Env, in *ListCardCountsInput) (*CardCountList, error) {
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
	rows, err := env.Q.ListCardCounts(ctx, dbgen.ListCardCountsParams{CardID: card.ID, ItemID: in.ItemID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := &CardCountList{Entries: []CardCountEntry{}}
	for _, r := range rows {
		out.Entries = append(out.Entries, countEntry(r.CardCountLedger, r.ItemName, r.ActorName, r.Reason))
	}
	return out, nil
}
