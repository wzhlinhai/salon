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
	"github.com/wzhlinhai/salon/backend/internal/inforce"
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

// change 是一次设置修改中选值用到的部分。
type change struct {
	key       string
	value     json.RawMessage
	isDefault bool
}

// history 是门店各项设置的修改历史，按 key 分组。
type history map[string]inforce.Timeline[change]

// loadHistory 读取门店的设置修改历史；key 不为空时只读这一项。
func loadHistory(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, key *string) (history, error) {
	rows, err := q.SettingHistory(ctx, dbgen.SettingHistoryParams{StoreID: storeID, Key: key})
	if err != nil {
		return nil, err
	}
	changes := make([]inforce.Change[change], 0, len(rows))
	for _, row := range rows {
		changes = append(changes, inforce.Change[change]{
			From: row.EffectiveFrom.Time, SavedAt: row.ChangedAt.Time, ID: row.ID,
			Value: change{key: row.Key, value: row.Value, isDefault: row.IsDefault},
		})
	}
	return inforce.Group(changes, func(c inforce.Change[change]) string { return c.Value.key }), nil
}

// inForce 是一项设置从某一天起执行的值。
type inForce struct {
	value     json.RawMessage
	isDefault bool
	// from 是这次修改的生效日期；没有修改时为零值。
	from time.Time
}

// resolve 把"没有修改"和"恢复默认"都换成目录默认值。
func resolve(d Def, c inforce.Change[change], ok bool) inForce {
	if !ok || c.Value.isDefault {
		return inForce{value: defaultJSON(d), isDefault: true, from: c.From}
	}
	return inForce{value: c.Value.value, from: c.From}
}

// at 返回设置 d 在营业日 day 执行的值。
func (h history) at(d Def, day time.Time) inForce {
	c, ok := h[d.Key].At(day)
	return resolve(d, c, ok)
}

// upcoming 返回设置 d 在营业日 day 之后安排好的值，每个生效日期一条，按日期升序。
func (h history) upcoming(d Def, day time.Time) []inForce {
	var out []inForce
	for _, c := range h[d.Key].After(day) {
		out = append(out, resolve(d, c, true))
	}
	return out
}

// Effective 返回门店在营业日 day 生效的设置。
func Effective(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, day time.Time) (Rules, error) {
	rules, err := EffectiveDays(ctx, q, storeID, []time.Time{day})
	if err != nil {
		return Rules{}, err
	}
	return rules[0], nil
}

// BusinessDate 返回时刻 t 属于哪一个营业日。
// 营业日 D 的结束时间取 D 那天生效的设置：次日结束时间之前的时刻仍属于 D。
func BusinessDate(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, t time.Time) (time.Time, error) {
	days, err := BusinessDates(ctx, q, storeID, []time.Time{t})
	if err != nil {
		return time.Time{}, err
	}
	return days[0], nil
}

func businessDate(t time.Time, end string) time.Time {
	local := t.In(Location)
	today := Date(local)
	prev := today.AddDate(0, 0, -1)
	if local.Format("15:04") < end {
		return prev
	}
	return today
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

// PerformanceRatio 返回设置键对应的业绩比例。
func (r Rules) PerformanceRatio(key string) int {
	var v int
	r.decode(key, &v)
	return v
}

// BusinessDates 批量按 BusinessDate 的相同规则换算时刻，设置历史只查询一次。
func BusinessDates(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, times []time.Time) ([]time.Time, error) {
	h, err := loadHistory(ctx, q, storeID, new(KeyBusinessDayEnd))
	if err != nil {
		return nil, err
	}
	def, _ := Lookup(KeyBusinessDayEnd)
	out := make([]time.Time, 0, len(times))
	for _, t := range times {
		// 营业日 D 的结束时间取 D 那天执行的设置：时刻 t 的日历日前一天执行的值。
		var end string
		if err := json.Unmarshal(h.at(def, Date(t).AddDate(0, 0, -1)).value, &end); err != nil {
			return nil, err
		}
		out = append(out, businessDate(t, end))
	}
	return out, nil
}

// AttendanceGraceMinutes 返回判断迟到、早退的宽限分钟数。
func (r Rules) AttendanceGraceMinutes() int {
	var v int
	r.decode(KeyAttendanceGraceMinutes, &v)
	return v
}

// EffectiveDays 一次加载设置历史，按输入顺序返回各营业日执行的设置。
func EffectiveDays(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, days []time.Time) ([]Rules, error) {
	h, err := loadHistory(ctx, q, storeID, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Rules, len(days))
	for i, day := range days {
		values := make(map[string]json.RawMessage, len(Catalogue))
		for _, d := range Catalogue {
			values[d.Key] = h.at(d, day).value
		}
		out[i] = rulesFrom(values)
	}
	return out, nil
}

// CollectionChannelEnabled 返回该收款通道是否启用。
func (r Rules) CollectionChannelEnabled(code string) bool {
	var value string
	r.decode("collection_channel_"+code, &value)
	return value == "on"
}

// DraftDualConfirm 返回金额草稿是否需要另一人确认。
func (r Rules) DraftDualConfirm() bool {
	var v string
	r.decode(KeyDraftDualConfirm, &v)
	return v == "on"
}

// BusinessDayDeadline 返回营业日 day 在次日的结束时刻，使用该营业日执行的设置。
func BusinessDayDeadline(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, day time.Time) (time.Time, error) {
	rules, err := Effective(ctx, q, storeID, day)
	if err != nil {
		return time.Time{}, err
	}
	clock, err := time.Parse("15:04", rules.BusinessDayEnd())
	if err != nil {
		return time.Time{}, err
	}
	next := day.AddDate(0, 0, 1)
	return time.Date(next.Year(), next.Month(), next.Day(), clock.Hour(), clock.Minute(), 0, 0, Location), nil
}
