// Package settings 是门店设置（ADR-0002）：按门店保存，带生效日期，修改留痕，下单时保存快照。
//
// 业务模块不直接读设置表，而是取某个营业日生效的 Rules（Effective），
// 或者下单时用 TakeSnapshot 保存一份、之后用 LoadSnapshot 读回，历史单据因此不随设置变化。
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	_ "time/tzdata" // 部署环境可能没有时区数据库。

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// Location 是门店所在时区。P0 只服务中国的门店。
var Location = mustLoadLocation("Asia/Shanghai")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// DateLayout 是营业日在接口中的格式。
const DateLayout = time.DateOnly

// Rules 是某个营业日生效的全部门店设置。没有设置的项是系统默认做法。
type Rules struct {
	values map[string]json.RawMessage
}

// rulesFrom 用 values 中的值覆盖系统默认做法。values 中不在目录里的项被忽略。
func rulesFrom(values map[string]json.RawMessage) Rules {
	r := Rules{values: make(map[string]json.RawMessage, len(Catalogue))}
	for _, d := range Catalogue {
		if v, ok := values[d.Key]; ok {
			r.values[d.Key] = v
		} else {
			r.values[d.Key] = defaultJSON(d)
		}
	}
	return r
}

func defaultJSON(d Def) json.RawMessage {
	b, err := json.Marshal(d.Default)
	if err != nil {
		panic(fmt.Sprintf("settings: default of %s: %v", d.Key, err))
	}
	return b
}

func (r Rules) decode(key string, v any) {
	if err := json.Unmarshal(r.values[key], v); err != nil {
		panic(fmt.Sprintf("settings: stored %s is invalid: %v", key, err))
	}
}

// BusinessHours 返回营业时段。
func (r Rules) BusinessHours() TimeRange {
	var v TimeRange
	r.decode(KeyBusinessHours, &v)
	return v
}

// BusinessDayEnd 返回营业日结束时间（"HH:MM"）：次日这个时刻之前发生的业务算前一天。
func (r Rules) BusinessDayEnd() string {
	var v string
	r.decode(KeyBusinessDayEnd, &v)
	return v
}

// MemberPriceRule 返回会员价与级别价的取价做法。
func (r Rules) MemberPriceRule() string {
	var v string
	r.decode(KeyMemberPriceRule, &v)
	return v
}

// PointsPerRechargeYuan 返回每积一分所需充值本金元数，零表示不积分。
func (r Rules) PointsPerRechargeYuan() int {
	var v int
	r.decode(KeyPointsPerRechargeYuan, &v)
	return v
}

// effectiveRow 是某项设置在某个营业日生效的那次修改。
type effectiveRow struct {
	value         json.RawMessage
	isDefault     bool
	effectiveFrom time.Time
}

func effectiveRows(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, day time.Time) (map[string]effectiveRow, error) {
	rows, err := q.EffectiveSettings(ctx, dbgen.EffectiveSettingsParams{StoreID: storeID, BusinessDate: pgDate(day)})
	if err != nil {
		return nil, err
	}
	out := make(map[string]effectiveRow, len(rows))
	for _, row := range rows {
		out[row.Key] = effectiveRow{value: row.Value, isDefault: row.IsDefault, effectiveFrom: row.EffectiveFrom.Time}
	}
	return out, nil
}

// configured 返回 key 在这一天设置的值；没有设置或恢复了默认时返回 false。
func configured(rows map[string]effectiveRow, key string) (effectiveRow, bool) {
	row, ok := rows[key]
	return row, ok && !row.isDefault
}

// Effective 返回门店在营业日 day 生效的设置。
func Effective(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, day time.Time) (Rules, error) {
	rows, err := effectiveRows(ctx, q, storeID, day)
	if err != nil {
		return Rules{}, err
	}
	values := map[string]json.RawMessage{}
	for _, d := range Catalogue {
		if row, ok := configured(rows, d.Key); ok {
			values[d.Key] = row.value
		}
	}
	return rulesFrom(values), nil
}

// BusinessDate 返回时刻 t 属于哪一个营业日。
// 营业日 D 的结束时间取 D 那天生效的设置：次日结束时间之前的时刻仍属于 D。
func BusinessDate(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, t time.Time) (time.Time, error) {
	local := t.In(Location)
	today := Date(local)
	prev := today.AddDate(0, 0, -1)
	rules, err := Effective(ctx, q, storeID, prev)
	if err != nil {
		return time.Time{}, err
	}
	if local.Format("15:04") < rules.BusinessDayEnd() {
		return prev, nil
	}
	return today, nil
}

// Date 返回 t 在门店时区的日历日期，表示为 UTC 零点。
func Date(t time.Time) time.Time {
	y, m, d := t.In(Location).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func pgDate(d time.Time) pgtype.Date { return pgtype.Date{Time: d, Valid: true} }

// CardDeductionOrder 返回卡余额支付先扣本金还是赠送金。
func (r Rules) CardDeductionOrder() string {
	var v string
	r.decode(KeyCardDeductionOrder, &v)
	return v
}

// OthersCardPayment 返回是否允许用别人的卡余额支付。
func (r Rules) OthersCardPayment() string {
	var v string
	r.decode(KeyOthersCardPayment, &v)
	return v
}

// CardCountOrder 返回扣次时先扣购买次数还是赠送次数。
func (r Rules) CardCountOrder() string {
	var v string
	r.decode(KeyCardCountOrder, &v)
	return v
}

// CardSelection 返回未指定卡余额支付的选卡规则。
func (r Rules) CardSelection() string {
	var v string
	r.decode(KeyCardSelection, &v)
	return v
}

// VoidWindowMinutes 返回撤单授权窗口的分钟数，零表示关闭。
func (r Rules) VoidWindowMinutes() int {
	var v int
	r.decode(KeyVoidWindowMinutes, &v)
	return v
}

// CardRefundBonus 返回退卡赠送金的处理规则。
func (r Rules) CardRefundBonus() string {
	var v string
	r.decode(KeyCardRefundBonus, &v)
	return v
}

// CardRefundFeePercent 返回退卡手续费百分比。
func (r Rules) CardRefundFeePercent() int {
	var v int
	r.decode(KeyCardRefundFeePercent, &v)
	return v
}

// PerformanceSplit 返回多人分业绩的做法。
func (r Rules) PerformanceSplit() string {
	var v string
	r.decode(KeyPerformanceSplit, &v)
	return v
}

// GuestCountSplit 返回每张结账单客次的归属做法。
func (r Rules) GuestCountSplit() string {
	var v string
	r.decode(KeyGuestCountSplit, &v)
	return v
}

// PerformanceRatio 返回支付方式的业绩比例。
func (r Rules) PerformanceRatio(category string) int {
	var key string
	switch category {
	case "count_paid":
		key = KeyPerformanceRatioCountPaid
	case "count_gift":
		key = KeyPerformanceRatioCountGift
	case "cash":
		key = KeyPerformanceRatioCash
	case "unionpay":
		key = KeyPerformanceRatioUnionpay
	case "wechat":
		key = KeyPerformanceRatioWechat
	case "alipay":
		key = KeyPerformanceRatioAlipay
	case "debt":
		key = KeyPerformanceRatioDebt
	case "card_principal":
		key = KeyPerformanceRatioCardPrincipal
	case "card_bonus":
		key = KeyPerformanceRatioCardBonus
	case "red_packet":
		key = KeyPerformanceRatioRedPacket
	default:
		panic("settings: unknown performance category: " + category)
	}
	var v int
	r.decode(key, &v)
	return v
}
