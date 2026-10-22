package cards

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// CreateCardUpgradeRuleInput 是新建升级规则的参数，条件种类后续可扩展。
type CreateCardUpgradeRuleInput struct {
	FromTypeID    uuid.UUID    `json:"from_type_id"`
	ToTypeID      uuid.UUID    `json:"to_type_id"`
	ConditionKind string       `json:"condition_kind" enum:"single_recharge,total_recharge"`
	Threshold     money.Amount `json:"threshold"`
}

// UpdateCardUpgradeRuleInput 是调整升级门槛或启停规则的参数。
type UpdateCardUpgradeRuleInput struct {
	ID        uuid.UUID    `json:"id"`
	Threshold money.Amount `json:"threshold"`
	Active    bool         `json:"active"`
}

// CardUpgradeRule 是门店设置的自动升级规则。
type CardUpgradeRule struct {
	CreateCardUpgradeRuleInput
	ID           uuid.UUID `json:"id"`
	FromTypeName string    `json:"from_type_name"`
	ToTypeName   string    `json:"to_type_name"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListCardUpgradeRulesInput 是列出本店升级规则的参数。
type ListCardUpgradeRulesInput struct{}

// CardUpgradeRuleList 是本店升级规则列表。
type CardUpgradeRuleList struct {
	Items []CardUpgradeRule `json:"items"`
}

// TopUpDifference 是手动升级的补差价和业绩归属人。
type TopUpDifference struct {
	Principal     money.Amount `json:"principal"`
	Bonus         money.Amount `json:"bonus"`
	SalesStaffIDs []uuid.UUID  `json:"sales_staff_ids,omitempty" maxItems:"10" uniqueItems:"true"`
}

// UpgradeCardInput 是手动升级储值卡的参数。
type UpgradeCardInput struct {
	TopUpDifference
	CardID   uuid.UUID `json:"card_id"`
	ToTypeID uuid.UUID `json:"to_type_id"`
}

// CardUpgradeType 是升级前后的卡类型。
type CardUpgradeType struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// RechargeResult 是充值后的卡及本次自动升级的目标。
type RechargeResult struct {
	Card
	UpgradedTo *CardUpgradeType `json:"upgraded_to,omitempty"`
}

// CardUpgrade 是只追加的手动或自动升级记录。
type CardUpgrade struct {
	ID           uuid.UUID       `json:"id"`
	TypeBefore   CardUpgradeType `json:"type_before"`
	TypeAfter    CardUpgradeType `json:"type_after"`
	Trigger      string          `json:"trigger"`
	RuleID       *uuid.UUID      `json:"rule_id,omitempty"`
	LedgerID     *uuid.UUID      `json:"ledger_id,omitempty"`
	BusinessDate string          `json:"business_date"`
	Actor        CardLedgerActor `json:"actor"`
	At           time.Time       `json:"at"`
}

// ListCardUpgradesInput 是升级记录的游标分页参数。
type ListCardUpgradesInput struct {
	CardID   uuid.UUID  `json:"card_id"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// CardUpgradeList 是由新到旧的升级记录。
type CardUpgradeList struct {
	Entries []CardUpgrade `json:"entries"`
}

func registerUpgrades(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListCardUpgradeRulesInput, CardUpgradeRuleList]{Name: "list_card_upgrade_rules", Summary: "列出升级规则", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect, Handle: listCardUpgradeRules})
	ops.Register(r, ops.Operation[CreateCardUpgradeRuleInput, CardUpgradeRule]{Name: "create_card_upgrade_rule", Summary: "新建升级规则", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: createCardUpgradeRule})
	ops.Register(r, ops.Operation[UpdateCardUpgradeRuleInput, CardUpgradeRule]{Name: "update_card_upgrade_rule", Summary: "编辑升级规则", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: updateCardUpgradeRule})
	ops.Register(r, ops.Operation[UpgradeCardInput, Card]{Name: "upgrade_card", Summary: "补差价升级储值卡", Kind: ops.Write, Permission: perm.CardSell, AI: ops.AIDraft, Handle: upgradeCard})
	ops.Register(r, ops.Operation[ListCardUpgradesInput, CardUpgradeList]{Name: "list_card_upgrades", Summary: "查看升级记录", Kind: ops.Read, Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listCardUpgrades})
}
func ruleView(r dbgen.CardUpgradeRule, from, to string) CardUpgradeRule {
	return CardUpgradeRule{CreateCardUpgradeRuleInput: CreateCardUpgradeRuleInput{FromTypeID: r.FromTypeID, ToTypeID: r.ToTypeID, ConditionKind: r.ConditionKind, Threshold: r.Threshold}, ID: r.ID, FromTypeName: from, ToTypeName: to, Active: r.Active, CreatedAt: r.CreatedAt.Time}
}
func listCardUpgradeRules(ctx context.Context, env ops.Env, _ *ListCardUpgradeRulesInput) (*CardUpgradeRuleList, error) {
	rows, err := env.Q.ListCardUpgradeRules(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &CardUpgradeRuleList{Items: []CardUpgradeRule{}}
	for _, r := range rows {
		out.Items = append(out.Items, ruleView(dbgen.CardUpgradeRule{ID: r.ID, FromTypeID: r.FromTypeID, ToTypeID: r.ToTypeID, ConditionKind: r.ConditionKind, Threshold: r.Threshold, Active: r.Active, CreatedAt: r.CreatedAt}, r.FromTypeName, r.ToTypeName))
	}
	return out, nil
}
func validateUpgradeRule(ctx context.Context, env ops.Env, in CreateCardUpgradeRuleInput, id uuid.UUID, active bool) (string, string, error) {
	if in.FromTypeID == in.ToTypeID || in.Threshold.Cmp(money.Amount{}) == 0 {
		return "", "", ops.Rule("升级类型须不同，门槛须大于零")
	}
	if in.ConditionKind != "single_recharge" && in.ConditionKind != "total_recharge" {
		return "", "", ops.Rule("升级条件无效")
	}
	ids := []uuid.UUID{in.FromTypeID, in.ToTypeID}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	names := map[uuid.UUID]string{}
	for _, id := range ids {
		ct, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return "", "", rowError(err)
		}
		if ct.Kind != StoredValue {
			return "", "", ops.Rule("升级规则仅支持储值卡")
		}
		if active && id == in.ToTypeID && !ct.Active {
			return "", "", ops.Rule("目标卡类型已停用")
		}
		names[id] = ct.Name
	}
	if active {
		rules, err := env.Q.ListCardUpgradeRules(ctx, env.Caller.StoreID)
		if err != nil {
			return "", "", err
		}
		edges := map[uuid.UUID][]uuid.UUID{}
		for _, r := range rules {
			if r.Active && r.ID != id {
				edges[r.FromTypeID] = append(edges[r.FromTypeID], r.ToTypeID)
			}
		}
		seen := map[uuid.UUID]bool{}
		var reaches func(uuid.UUID) bool
		reaches = func(node uuid.UUID) bool {
			if node == in.FromTypeID {
				return true
			}
			if seen[node] {
				return false
			}
			seen[node] = true
			for _, to := range edges[node] {
				if reaches(to) {
					return true
				}
			}
			return false
		}
		if reaches(in.ToTypeID) {
			return "", "", ops.Rule("升级规则不能形成环路")
		}
	}
	return names[in.FromTypeID], names[in.ToTypeID], nil
}
func upgradeRuleError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ops.Rule("升级规则已存在")
	}
	return err
}
func createCardUpgradeRule(ctx context.Context, env ops.Env, in *CreateCardUpgradeRuleInput) (*CardUpgradeRule, error) {
	// 没有规则行时也要排队，避免两个并发新增各自看不到对方而组成环路。
	if err := env.Q.LockCardUpgradeRules(ctx, env.Caller.StoreID.String()); err != nil {
		return nil, err
	}
	from, to, err := validateUpgradeRule(ctx, env, *in, uuid.Nil, true)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateCardUpgradeRule(ctx, dbgen.CreateCardUpgradeRuleParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, FromTypeID: in.FromTypeID, ToTypeID: in.ToTypeID, ConditionKind: in.ConditionKind, Threshold: in.Threshold})
	if err != nil {
		return nil, upgradeRuleError(err)
	}
	out := ruleView(row, from, to)
	if err := audit.Record(ctx, env, "create_card_upgrade_rule", audit.CardUpgradeRuleTarget(row.ID), nil, out); err != nil {
		return nil, err
	}
	return &out, nil
}
func updateCardUpgradeRule(ctx context.Context, env ops.Env, in *UpdateCardUpgradeRuleInput) (*CardUpgradeRule, error) {
	if err := env.Q.LockCardUpgradeRules(ctx, env.Caller.StoreID.String()); err != nil {
		return nil, err
	}
	old, err := env.Q.GetCardUpgradeRule(ctx, dbgen.GetCardUpgradeRuleParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	input := CreateCardUpgradeRuleInput{FromTypeID: old.FromTypeID, ToTypeID: old.ToTypeID, ConditionKind: old.ConditionKind, Threshold: in.Threshold}
	from, to, err := validateUpgradeRule(ctx, env, input, in.ID, in.Active)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.UpdateCardUpgradeRule(ctx, dbgen.UpdateCardUpgradeRuleParams{ID: in.ID, Threshold: in.Threshold, Active: in.Active})
	if err != nil {
		return nil, err
	}
	out := ruleView(row, from, to)
	if err := audit.Record(ctx, env, "update_card_upgrade_rule", audit.CardUpgradeRuleTarget(row.ID), ruleView(old, from, to), out); err != nil {
		return nil, err
	}
	return &out, nil
}
func upgradeCard(ctx context.Context, env ops.Env, in *UpgradeCardInput) (*Card, error) {
	if in.Principal.Cmp(money.Amount{}) != 0 || in.Bonus.Cmp(money.Amount{}) != 0 || len(in.SalesStaffIDs) > 0 {
		if err := lockSalesStaff(ctx, env, in.SalesStaffIDs); err != nil {
			return nil, err
		}
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
	if before.Kind != StoredValue {
		return nil, ops.Rule("仅储值卡支持升级")
	}
	ct, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: in.ToTypeID})
	if err != nil {
		return nil, rowError(err)
	}
	if !ct.Active || ct.Kind != StoredValue || ct.ID == card.CardTypeID {
		return nil, ops.Rule("目标须为启用的其他储值卡类型")
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	ledger, err := appendLedger(ctx, env, card.ID, "upgrade", before.Principal, before.Bonus, TopUpInput{Principal: in.Principal, Bonus: in.Bonus, SalesStaffIDs: in.SalesStaffIDs}, day)
	if err != nil {
		return nil, err
	}
	if err := recordUpgrade(ctx, env, card, ct.ID, "manual", nil, ledger.ID, day); err != nil {
		return nil, err
	}
	out, err := cardView(ctx, env, card.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "upgrade_card", audit.CardTarget(card.ID, card.CustomerID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}
func recordUpgrade(ctx context.Context, env ops.Env, card dbgen.Card, to uuid.UUID, trigger string, rule *uuid.UUID, ledger uuid.UUID, day time.Time) error {
	// 升级不改有效期；门店需要更正时另写调整单。
	if err := env.Q.UpdateUpgradedCard(ctx, dbgen.UpdateUpgradedCardParams{ID: card.ID, CardTypeID: to}); err != nil {
		return err
	}
	return env.Q.AddCardUpgrade(ctx, dbgen.AddCardUpgradeParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, TypeBefore: card.CardTypeID, TypeAfter: to, Trigger: trigger, RuleID: rule, LedgerID: &ledger, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
}
func autoUpgradeCard(ctx context.Context, env ops.Env, card dbgen.Card, principal money.Amount, ledgerID uuid.UUID, day time.Time) (*CardUpgradeType, error) {
	rows, err := env.Q.ActiveCardUpgradeRules(ctx, dbgen.ActiveCardUpgradeRulesParams{StoreID: env.Caller.StoreID, FromTypeID: card.CardTypeID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	total, err := env.Q.TotalCardRecharge(ctx, card.ID)
	if err != nil {
		return nil, err
	}
	var cumulative money.Total
	if err := cumulative.Scan(total); err != nil {
		return nil, err
	}
	for _, r := range rows {
		met := r.ConditionKind == "single_recharge" && principal.Cmp(r.Threshold) >= 0 || r.ConditionKind == "total_recharge" && cumulative.Cmp(r.Threshold) >= 0
		if !met {
			continue
		}
		target, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: r.ToTypeID})
		if err != nil {
			return nil, rowError(err)
		}
		if !target.Active {
			continue
		}
		before, err := cardView(ctx, env, card.ID)
		if err != nil {
			return nil, err
		}
		if err := recordUpgrade(ctx, env, card, target.ID, "auto", &r.ID, ledgerID, day); err != nil {
			return nil, err
		}
		after, err := cardView(ctx, env, card.ID)
		if err != nil {
			return nil, err
		}
		if err := audit.Record(ctx, env, "auto_upgrade_card", audit.CardTarget(card.ID, card.CustomerID), before, after); err != nil {
			return nil, err
		}
		return &CardUpgradeType{ID: target.ID, Name: target.Name}, nil
	}
	return nil, nil
}
func listCardUpgrades(ctx context.Context, env ops.Env, in *ListCardUpgradesInput) (*CardUpgradeList, error) {
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
	rows, err := env.Q.ListCardUpgrades(ctx, dbgen.ListCardUpgradesParams{CardID: card.ID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := &CardUpgradeList{Entries: []CardUpgrade{}}
	for _, r := range rows {
		u := r.CardUpgrade
		out.Entries = append(out.Entries, CardUpgrade{ID: u.ID, TypeBefore: CardUpgradeType{ID: u.TypeBefore, Name: r.TypeBeforeName}, TypeAfter: CardUpgradeType{ID: u.TypeAfter, Name: r.TypeAfterName}, Trigger: u.Trigger, RuleID: u.RuleID, LedgerID: u.LedgerID, BusinessDate: u.BusinessDate.Time.Format(time.DateOnly), Actor: CardLedgerActor{ID: u.ActorID, Name: r.ActorName}, At: u.At.Time})
	}
	return out, nil
}
