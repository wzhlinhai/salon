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

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Register 声明卡类型、开卡、充值和卡流水操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListCardPointsInput, PointsLedgerList]{Name: "list_card_points", Summary: "查看积分流水", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCardPoints})
	registerUpgrades(r)
	registerTransfers(r)
	ops.Register(r, ops.Operation[ListCardCountsInput, CardCountList]{Name: "list_card_counts", Summary: "查看次数流水", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCardCounts})
	ops.Register(r, ops.Operation[AdjustCardInput, Card]{Name: "adjust_card", Summary: "更正卡并追加调整单", Kind: ops.Write, Permission: perm.CardAdjust, AI: ops.AIDraft, Handle: adjustCard})
	ops.Register(r, ops.Operation[ListCardAdjustmentsInput, CardAdjustmentList]{Name: "list_card_adjustments", Summary: "查看卡调整单", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCardAdjustments})
	ops.Register(r, ops.Operation[ListCardTypesInput, CardTypeList]{Name: "list_card_types", Summary: "列出本店卡类型", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect, Handle: listCardTypes})
	ops.Register(r, ops.Operation[CreateCardTypeInput, CardType]{Name: "create_card_type", Summary: "新建卡类型", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: createCardType})
	ops.Register(r, ops.Operation[UpdateCardTypeInput, CardType]{Name: "update_card_type", Summary: "编辑或停用卡类型", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: updateCardType})
	ops.Register(r, ops.Operation[OpenCardInput, Card]{Name: "open_card", Summary: "为顾客开卡并记录首次充值", Kind: ops.Write, Permission: perm.CardSell, AI: ops.AIDraft, Handle: openCard})
	ops.Register(r, ops.Operation[RechargeInput, RechargeResult]{Name: "recharge_card", Summary: "充值并追加卡流水", Kind: ops.Write, Permission: perm.CardSell, AI: ops.AIDraft, Handle: rechargeCard})
	ops.Register(r, ops.Operation[ListCustomerCardsInput, CardList]{Name: "list_customer_cards", Summary: "查看顾客在发行门店的卡", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCustomerCards})
	ops.Register(r, ops.Operation[ListCardLedgerInput, CardLedgerList]{Name: "list_card_ledger", Summary: "查看卡流水和业绩归属人", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCardLedger})
}
func listCardTypes(ctx context.Context, env ops.Env, _ *ListCardTypesInput) (*CardTypeList, error) {
	rows, err := env.Q.ListCardTypes(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &CardTypeList{Items: []CardType{}}
	for _, r := range rows {
		out.Items = append(out.Items, typeView(r))
	}
	if err := typeItems(ctx, env, out.Items); err != nil {
		return nil, err
	}
	return out, nil
}
func lockSalesStaff(ctx context.Context, env ops.Env, ids []uuid.UUID) error {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if len(ids) < 1 || len(ids) > 10 || len(slices.Compact(ordered)) != len(ids) {
		return ops.Rule("请选择一到十名不同的业绩归属人")
	}
	for _, id := range ordered {
		staff, err := env.Q.ShareCustomerStaff(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!staff.Active || staff.StoreID != env.Caller.StoreID)) {
			return ops.Rule("业绩归属人必须是本店在职员工")
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func canView(ctx context.Context, env ops.Env, id uuid.UUID) error {
	c, err := env.Q.GetCustomer(ctx, id)
	if err != nil {
		return rowError(err)
	}
	if !customers.CanView(env.Caller, c) {
		return ops.ErrNotFound
	}
	return nil
}
func view(r dbgen.Card, name string, principal, bonus money.Amount) Card {
	return Card{ID: r.ID, CustomerID: r.CustomerID, CardTypeID: r.CardTypeID, CardTypeName: name, OpenedAt: r.OpenedAt.Time, ExpiresOn: dateView(r.ExpiresOn), Principal: principal, Bonus: bonus}
}
func cardView(ctx context.Context, env ops.Env, id uuid.UUID) (*Card, error) {
	r, err := env.Q.CardView(ctx, dbgen.CardViewParams{StoreID: env.Caller.StoreID, ID: id})
	if err != nil {
		return nil, rowError(err)
	}
	out := view(r.Card, r.CardTypeName, r.PrincipalAfter, r.BonusAfter)
	out.Kind = r.Kind
	views := []Card{out}
	if err := fillCounts(ctx, env, views); err != nil {
		return nil, err
	}
	return &views[0], nil
}
func openCard(ctx context.Context, env ops.Env, in *OpenCardInput) (*Card, error) {
	return openCardAt(ctx, env, in, time.Now())
}
func openCardAt(ctx context.Context, env ops.Env, in *OpenCardInput, now time.Time) (*Card, error) {
	if err := lockSalesStaff(ctx, env, in.SalesStaffIDs); err != nil {
		return nil, err
	}
	customer, err := env.Q.ShareCardCustomer(ctx, dbgen.ShareCardCustomerParams{StoreID: env.Caller.StoreID, ID: in.CustomerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.Rule("没有本店顾客")
	}
	if err != nil {
		return nil, err
	}
	if !customers.CanView(env.Caller, customer) {
		return nil, ops.ErrNotFound
	}
	if customer.MergedInto != nil {
		return nil, ops.Rule("该顾客已合并到另一位顾客")
	}
	ct, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: in.CardTypeID})
	if err != nil {
		return nil, rowError(err)
	}
	if !ct.Active {
		return nil, ops.Rule("卡类型已停用")
	}
	if ct.Kind == CountCard && (in.Principal.Cmp(money.Amount{}) != 0 || in.Bonus.Cmp(money.Amount{}) != 0) {
		return nil, ops.Rule("次卡本金和赠送金必须为零")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
	if err != nil {
		return nil, err
	}
	expires := ct.ValidityDate
	if ct.ValidityKind == "days_after_open" {
		expires = pgtype.Date{Time: day.AddDate(0, 0, int(*ct.ValidityDays)), Valid: true}
	}
	card, err := env.Q.CreateCard(ctx, dbgen.CreateCardParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: in.CustomerID, CardTypeID: in.CardTypeID, OpenedAt: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresOn: expires, CreatedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	if ct.Kind != StoredValue {
		if err := openCounts(ctx, env, card.ID, ct.ID, day); err != nil {
			return nil, err
		}
	}
	// 次卡也写零余额开卡流水，保证每张卡都有余额行并兼容现有视图。
	if _, err := appendLedger(ctx, env, card.ID, "open", money.Amount{}, money.Amount{}, in.TopUpInput, day); err != nil {
		return nil, err
	}
	out, err := cardView(ctx, env, card.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "open_card", audit.CardTarget(card.ID, card.CustomerID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}
func rechargeCard(ctx context.Context, env ops.Env, in *RechargeInput) (*RechargeResult, error) {
	if err := lockSalesStaff(ctx, env, in.SalesStaffIDs); err != nil {
		return nil, err
	}
	card, err := env.Q.LockCard(ctx, dbgen.LockCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := canView(ctx, env, card.CustomerID); err != nil {
		return nil, err
	}
	before, err := cardView(ctx, env, card.ID)
	if err != nil {
		return nil, err
	}
	if before.Kind == CountCard {
		return nil, ops.Rule("次卡不能充值")
	}
	if in.Principal.Cmp(money.Amount{}) == 0 && in.Bonus.Cmp(money.Amount{}) == 0 {
		return nil, ops.Rule("充值本金与赠送金合计必须大于零")
	}
	previous, err := env.Q.LatestCardLedger(ctx, card.ID)
	if err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	ledger, err := appendLedger(ctx, env, card.ID, "recharge", previous.PrincipalAfter, previous.BonusAfter, in.TopUpInput, day)
	if err != nil {
		return nil, err
	}
	upgraded, err := autoUpgradeCard(ctx, env, card, in.Principal, ledger.ID, day)
	if err != nil {
		return nil, err
	}
	out, err := cardView(ctx, env, card.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "recharge_card", audit.CardTarget(card.ID, card.CustomerID), before, out); err != nil {
		return nil, err
	}
	return &RechargeResult{Card: *out, UpgradedTo: upgraded}, nil
}
func appendLedger(ctx context.Context, env ops.Env, id uuid.UUID, kind string, principal, bonus money.Amount, in TopUpInput, day time.Time) (dbgen.CardLedger, error) {
	pa, err := principal.Add(in.Principal)
	if err != nil {
		return dbgen.CardLedger{}, ops.Rule("卡余额超过上限")
	}
	ba, err := bonus.Add(in.Bonus)
	if err != nil {
		return dbgen.CardLedger{}, ops.Rule("卡余额超过上限")
	}
	row, err := env.Q.AddCardLedger(ctx, dbgen.AddCardLedgerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: id, Kind: kind, PrincipalBefore: principal, PrincipalDelta: in.Principal.Sub(money.Amount{}), PrincipalAfter: pa, BonusBefore: bonus, BonusDelta: in.Bonus.Sub(money.Amount{}), BonusAfter: ba, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
	if err != nil {
		return dbgen.CardLedger{}, err
	}
	for _, staff := range in.SalesStaffIDs {
		if err := env.Q.AddCardLedgerSales(ctx, dbgen.AddCardLedgerSalesParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, LedgerID: row.ID, StaffID: staff}); err != nil {
			return dbgen.CardLedger{}, err
		}
	}
	if err := earnPoints(ctx, env, row, in.Principal); err != nil {
		return dbgen.CardLedger{}, err
	}
	return row, nil
}
func listCustomerCards(ctx context.Context, env ops.Env, in *ListCustomerCardsInput) (*CardList, error) {
	if err := canView(ctx, env, in.CustomerID); err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCustomerCards(ctx, dbgen.ListCustomerCardsParams{StoreID: env.Caller.StoreID, CustomerID: in.CustomerID})
	if err != nil {
		return nil, err
	}
	out := &CardList{Items: []Card{}}
	for _, r := range rows {
		item := view(r.Card, r.CardTypeName, r.PrincipalAfter, r.BonusAfter)
		item.Kind = r.Kind
		out.Items = append(out.Items, item)
	}
	if err := fillCounts(ctx, env, out.Items); err != nil {
		return nil, err
	}
	return out, nil
}
func listCardLedger(ctx context.Context, env ops.Env, in *ListCardLedgerInput) (*CardLedgerList, error) {
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
	rows, err := env.Q.ListCardLedger(ctx, dbgen.ListCardLedgerParams{CardID: in.CardID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.CardLedger.ID
	}
	sales, err := env.Q.ListCardLedgerSales(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID][]SalesStaff{}
	for _, s := range sales {
		byID[s.LedgerID] = append(byID[s.LedgerID], SalesStaff{ID: s.ID, Name: s.Name})
	}
	out := &CardLedgerList{Entries: []CardLedgerEntry{}}
	for _, r := range rows {
		l := r.CardLedger
		staff := byID[l.ID]
		if staff == nil {
			staff = []SalesStaff{}
		}
		out.Entries = append(out.Entries, CardLedgerEntry{ID: l.ID, Kind: l.Kind, Reason: r.Reason, OtherCardID: optionalCardID(r.OtherCardID), PrincipalBefore: l.PrincipalBefore, PrincipalDelta: l.PrincipalDelta, PrincipalAfter: l.PrincipalAfter, BonusBefore: l.BonusBefore, BonusDelta: l.BonusDelta, BonusAfter: l.BonusAfter, BusinessDate: l.BusinessDate.Time.Format(time.DateOnly), At: l.At.Time, Actor: CardLedgerActor{ID: l.ActorID, Name: r.ActorName}, SalesStaff: staff})
	}
	return out, nil
}

func optionalCardID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
