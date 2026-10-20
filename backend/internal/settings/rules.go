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
