package orders

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// DraftPreview 是开单类草稿的只读预览：逐行价格和顾客可用的卡。
type DraftPreview struct {
	Lines []DraftPreviewLine `json:"lines"`
	Cards []DraftPreviewCard `json:"cards"`
}

// DraftPreviewLine 是一行项目的预览，价格按第一个岗位的员工计算。
type DraftPreviewLine struct {
	ItemID      uuid.UUID           `json:"item_id"`
	ItemName    string              `json:"item_name"`
	Price       money.Amount        `json:"price"`
	PriceSource string              `json:"price_source" enum:"standard,level,member"`
	LevelID     *uuid.UUID          `json:"level_id,omitempty"`
	Staff       []DraftPreviewStaff `json:"staff"`
}

// DraftPreviewStaff 是项目岗位上的员工。
type DraftPreviewStaff struct {
	Slot      int16     `json:"slot"`
	StaffID   uuid.UUID `json:"staff_id"`
	StaffName string    `json:"staff_name"`
}

// DraftPreviewCard 是顾客一张可用卡的余额、次数和每行会员价。
type DraftPreviewCard struct {
	CardID       uuid.UUID                 `json:"card_id"`
	CardTypeID   uuid.UUID                 `json:"card_type_id"`
	CardTypeName string                    `json:"card_type_name"`
	Kind         string                    `json:"kind"`
	Principal    money.Amount              `json:"principal"`
	Bonus        money.Amount              `json:"bonus"`
	Balance      money.Amount              `json:"balance"`
	Counts       []DraftPreviewCardCount   `json:"counts"`
	MemberPrices []DraftPreviewMemberPrice `json:"member_prices"`
}

// DraftPreviewCardCount 是卡上某个项目的剩余次数。
type DraftPreviewCardCount struct {
	ItemID   uuid.UUID `json:"item_id"`
	ItemName string    `json:"item_name"`
	Paid     int32     `json:"paid"`
	Gift     int32     `json:"gift"`
}

// DraftPreviewMemberPrice 是某张卡对某一行按第一个岗位员工计算的适用价格。
type DraftPreviewMemberPrice struct {
	Line        int          `json:"line"`
	ItemID      uuid.UUID    `json:"item_id"`
	Price       money.Amount `json:"price"`
	PriceSource string       `json:"price_source" enum:"standard,level,member"`
	LevelID     *uuid.UUID   `json:"level_id,omitempty"`
	CardTypeID  *uuid.UUID   `json:"card_type_id,omitempty"`
}

// PreviewOpenOrder 用当前生效规则或补录日规则预览一张开单草稿，供团购开单复用。
func PreviewOpenOrder(ctx context.Context, env ops.Env, in OpenOrderInput) (*DraftPreview, error) {
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	if in.PaperDate != nil {
		day, err = backfillDay(ctx, env, *in.PaperDate)
		if err != nil {
			return nil, err
		}
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	return buildDraftPreview(ctx, env, in.CustomerID, in.Items, rules, day)
}

func previewOpenOrder(ctx context.Context, env ops.Env, params json.RawMessage) (any, error) {
	var in OpenOrderInput
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	return PreviewOpenOrder(ctx, env, in)
}

func previewAddOrderItem(ctx context.Context, env ops.Env, params json.RawMessage) (any, error) {
	var in AddOrderItemInput
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	order, err := env.Q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: env.Caller.StoreID, ID: in.OrderID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := checkOrderState(order, "open"); err != nil {
		return nil, err
	}
	if err := checkOrderChange(ctx, env, order, env.Now); err != nil {
		return nil, err
	}
	rules, err := settings.LoadSnapshot(ctx, env.Q, env.Caller.StoreID, order.SettingsSnapshotID)
	if err != nil {
		return nil, err
	}
	return buildDraftPreview(ctx, env, &order.CustomerID, []OrderItemInput{in.Item}, rules, order.BusinessDate.Time)
}

func buildDraftPreview(ctx context.Context, env ops.Env, customerID *uuid.UUID, in []OrderItemInput, rules settings.Rules, day time.Time) (*DraftPreview, error) {
	itemIDs := []uuid.UUID{}
	staffIDs := []uuid.UUID{}
	for _, line := range in {
		itemIDs = append(itemIDs, line.ItemID)
		for _, staff := range line.Staff {
			staffIDs = append(staffIDs, staff.StaffID)
		}
	}
	itemIDs = compactIDs(itemIDs)
	staffIDs = compactIDs(staffIDs)
	itemRows, err := env.Q.GetItems(ctx, dbgen.GetItemsParams{StoreID: env.Caller.StoreID, Ids: itemIDs})
	if err != nil {
		return nil, err
	}
	items := map[uuid.UUID]dbgen.Item{}
	for _, row := range itemRows {
		items[row.ID] = row
	}
	for _, id := range itemIDs {
		if _, ok := items[id]; !ok {
			return nil, ops.ErrNotFound
		}
	}
	positions, err := env.Q.ListItemPositions(ctx, dbgen.ListItemPositionsParams{StoreID: env.Caller.StoreID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	slots := map[uuid.UUID]map[int16]bool{}
	for _, p := range positions {
		if slots[p.ItemID] == nil {
			slots[p.ItemID] = map[int16]bool{}
		}
		slots[p.ItemID][p.Slot] = true
	}
	levels, err := env.Q.ListItemLevelPrices(ctx, dbgen.ListItemLevelPricesParams{StoreID: env.Caller.StoreID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	levelPrices := map[uuid.UUID]map[uuid.UUID]money.Amount{}
	for _, row := range levels {
		if levelPrices[row.ItemID] == nil {
			levelPrices[row.ItemID] = map[uuid.UUID]money.Amount{}
		}
		levelPrices[row.ItemID][row.LevelID] = row.Price
	}
	staffRows, err := env.Q.StaffLevels(ctx, dbgen.StaffLevelsParams{StoreID: env.Caller.StoreID, Ids: staffIDs})
	if err != nil {
		return nil, err
	}
	staff := map[uuid.UUID]dbgen.StaffLevelsRow{}
	for _, row := range staffRows {
		staff[row.ID] = row
	}
	out := &DraftPreview{Lines: make([]DraftPreviewLine, 0, len(in)), Cards: []DraftPreviewCard{}}
	for _, line := range in {
		item := items[line.ItemID]
		preview := DraftPreviewLine{ItemID: item.ID, ItemName: item.Name, Staff: []DraftPreviewStaff{}}
		seen := map[int16]bool{}
		for _, assigned := range line.Staff {
			if !slots[item.ID][assigned.Slot] {
				return nil, ops.Rule("项目没有这个岗位")
			}
			if seen[assigned.Slot] {
				return nil, ops.Rule("同一岗位不能重复填员工")
			}
			seen[assigned.Slot] = true
			employee, ok := staff[assigned.StaffID]
			if !ok {
				return nil, ops.Rule("岗位上的员工必须是本店在职员工")
			}
			preview.Staff = append(preview.Staff, DraftPreviewStaff{Slot: assigned.Slot, StaffID: employee.ID, StaffName: employee.Name})
		}
		first := slotOneStaff(line.Staff)
		var levelID *uuid.UUID
		var levelPrice *money.Amount
		if first != nil {
			if employee, ok := staff[first.StaffID]; ok {
				levelID = employee.LevelID
				if levelID != nil {
					if price, ok := levelPrices[item.ID][*levelID]; ok {
						levelPrice = &price
					}
				}
			}
		}
		price := catalog.PriceFor(item, levelID, levelPrice, nil, nil, rules.MemberPriceRule())
		preview.Price, preview.PriceSource, preview.LevelID = price.Price, price.Source, price.LevelID
		out.Lines = append(out.Lines, preview)
	}
	if customerID == nil {
		return out, nil
	}
	if err := customers.CheckViewable(ctx, env, *customerID); err != nil {
		if errors.Is(err, ops.ErrNotFound) {
			return out, nil
		}
		return nil, err
	}
	cardRows, err := env.Q.ListCustomerCards(ctx, dbgen.ListCustomerCardsParams{StoreID: env.Caller.StoreID, CustomerID: *customerID})
	if err != nil {
		return nil, err
	}
	cardIDs := make([]uuid.UUID, len(cardRows))
	for i, row := range cardRows {
		cardIDs[i] = row.Card.ID
	}
	countRows, err := env.Q.RemainingCounts(ctx, cardIDs)
	if err != nil {
		return nil, err
	}
	counts := map[uuid.UUID][]dbgen.RemainingCountsRow{}
	for _, row := range countRows {
		counts[row.CardItem.CardID] = append(counts[row.CardItem.CardID], row)
	}
	wantedItems := map[uuid.UUID]bool{}
	for _, id := range itemIDs {
		wantedItems[id] = true
	}
	members, err := env.Q.ListItemMemberPrices(ctx, dbgen.ListItemMemberPricesParams{StoreID: env.Caller.StoreID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	memberPrices := map[uuid.UUID]map[uuid.UUID]money.Amount{}
	for _, row := range members {
		if memberPrices[row.ItemID] == nil {
			memberPrices[row.ItemID] = map[uuid.UUID]money.Amount{}
		}
		memberPrices[row.ItemID][row.CardTypeID] = row.Price
	}
	for _, row := range cardRows {
		if row.Card.Status != "active" {
			continue
		}
		if row.Card.ExpiresOn.Valid && row.Card.ExpiresOn.Time.Before(day) {
			continue
		}
		balance, err := row.PrincipalAfter.Add(row.BonusAfter)
		if err != nil {
			return nil, err
		}
		hasBalance := row.Kind != cards.CountCard && balance.Cmp(money.Amount{}) > 0
		card := DraftPreviewCard{CardID: row.Card.ID, CardTypeID: row.Card.CardTypeID, CardTypeName: row.CardTypeName, Kind: row.Kind, Principal: row.PrincipalAfter, Bonus: row.BonusAfter, Balance: balance, Counts: []DraftPreviewCardCount{}, MemberPrices: []DraftPreviewMemberPrice{}}
		hasCounts := false
		for _, count := range counts[row.Card.ID] {
			if !wantedItems[count.CardItem.ItemID] || count.PaidAfter == 0 && count.GiftAfter == 0 {
				continue
			}
			hasCounts = true
			card.Counts = append(card.Counts, DraftPreviewCardCount{ItemID: count.CardItem.ItemID, ItemName: count.ItemName, Paid: count.PaidAfter, Gift: count.GiftAfter})
		}
		if !hasBalance && !hasCounts {
			continue
		}
		if row.Kind != cards.CountCard {
			for i, line := range out.Lines {
				item := items[line.ItemID]
				member, ok := memberPrices[item.ID][row.Card.CardTypeID]
				if !ok {
					continue
				}
				var levelPrice *money.Amount
				if line.LevelID != nil {
					if price, ok := levelPrices[item.ID][*line.LevelID]; ok {
						levelPrice = &price
					}
				}
				price := catalog.PriceFor(item, line.LevelID, levelPrice, &member, &row.Card.CardTypeID, rules.MemberPriceRule())
				card.MemberPrices = append(card.MemberPrices, DraftPreviewMemberPrice{Line: i, ItemID: item.ID, Price: price.Price, PriceSource: price.Source, LevelID: price.LevelID, CardTypeID: price.CardTypeID})
			}
		}
		out.Cards = append(out.Cards, card)
	}
	return out, nil
}

func slotOneStaff(staff []OrderItemStaffInput) *OrderItemStaffInput {
	for i := range staff {
		if staff[i].Slot == 1 {
			return &staff[i]
		}
	}
	return nil
}

func compactIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
