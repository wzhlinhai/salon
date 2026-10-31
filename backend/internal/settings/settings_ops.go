package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Setting 是一项门店设置在某个营业日的状态。
type Setting struct {
	Options   []ChoiceOption `json:"options,omitempty"`
	Min       int            `json:"min"`
	Max       int            `json:"max"`
	Key       string         `json:"key"`
	Label     string         `json:"label"`
	Hint      string         `json:"hint"`
	Kind      Kind           `json:"kind" enum:"time,time_range,choice,integer" doc:"值的形式：时刻、时段、选项或整数"`
	Value     any            `json:"value" doc:"这一天执行的值"`
	Default   any            `json:"default" doc:"系统默认做法"`
	IsDefault bool           `json:"is_default" doc:"这一天用的是系统默认做法"`
	// EffectiveFrom 是这一天执行的值从哪一天开始生效；用系统默认做法时为空。
	EffectiveFrom *string `json:"effective_from,omitempty" format:"date"`
	// Upcoming 是这一天之后已经安排好、尚未生效的修改。
	Upcoming []ScheduledValue `json:"upcoming"`
}

// ScheduledValue 是一项设置从某天开始执行的值。
type ScheduledValue struct {
	EffectiveFrom string `json:"effective_from" format:"date"`
	Value         any    `json:"value"`
	IsDefault     bool   `json:"is_default" doc:"从这一天起恢复系统默认做法"`
}

// SettingList 是 get_settings 的返回值。
type SettingList struct {
	BusinessDate string    `json:"business_date" format:"date" doc:"查看的营业日"`
	Settings     []Setting `json:"settings"`
}

// GetSettingsInput 是 get_settings 的参数。
type GetSettingsInput struct {
	Date string `json:"date,omitempty" format:"date" doc:"查看哪一个营业日执行的设置，不填为今天"`
}

// ChangeSettingInput 是 change_setting 的参数。
type ChangeSettingInput struct {
	Key string `json:"key" minLength:"1" doc:"设置项，见 get_settings"`
	// Value 为 null 表示恢复系统默认做法。
	Value         any    `json:"value" doc:"新的值，形式见 get_settings 的 kind；null 表示恢复系统默认做法"`
	EffectiveFrom string `json:"effective_from" format:"date" doc:"从哪一个营业日开始执行，不能早于今天"`
}

// SettingChange 是一次设置修改的记录。
type SettingChange struct {
	ID    uuid.UUID `json:"id"`
	Key   string    `json:"key"`
	Label string    `json:"label"`
	// OldValue 是修改前在生效日期那天执行的值。
	OldValue      any       `json:"old_value" doc:"修改前在生效日期那天执行的值"`
	OldIsDefault  bool      `json:"old_is_default" doc:"修改前用的是系统默认做法"`
	Value         any       `json:"value" doc:"修改后的值"`
	IsDefault     bool      `json:"is_default" doc:"这次修改是恢复系统默认做法"`
	EffectiveFrom string    `json:"effective_from" format:"date"`
	ChangedBy     staff.Ref `json:"changed_by" doc:"修改人"`
	ChangedAt     time.Time `json:"changed_at"`
}

// ListSettingChangesInput 是 list_setting_changes 的参数。
type ListSettingChangesInput struct {
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty" doc:"上一页最后一条的 ID，只看比它更早的；不填为最新一页"`
	Key      *string    `json:"key,omitempty" doc:"只看这一项设置的修改，不填为全部"`
}

// SettingChangeList 是 list_setting_changes 的返回值，最新的修改在前。
type SettingChangeList struct {
	Changes []SettingChange `json:"changes"`
}

// Register 声明门店设置的操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[GetSettingsInput, SettingList]{
		Name: "get_settings", Summary: "查看门店设置在某个营业日执行的值、系统默认做法和已安排的修改", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *GetSettingsInput) (*SettingList, error) {
			day, err := dayOrToday(ctx, env, in.Date)
			if err != nil {
				return nil, err
			}
			return listSettings(ctx, env, day)
		},
	})
	ops.Register(r, ops.Operation[ChangeSettingInput, SettingChange]{
		Name: "change_setting", Summary: "修改一项门店设置，从指定的营业日开始执行；之前的业务和已下的单不受影响", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *ChangeSettingInput) (*SettingChange, error) {
			return changeSetting(ctx, env, in)
		},
	})
	ops.Register(r, ops.Operation[ListSettingChangesInput, SettingChangeList]{
		Name: "list_setting_changes", Summary: "查看门店设置的修改记录：谁、何时、改前改后、从哪天生效", Kind: ops.Read,
		Permission: perm.AuditLogView, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *ListSettingChangesInput) (*SettingChangeList, error) {
			limit := in.Limit
			if limit == 0 {
				limit = 50
			}
			rows, err := env.Q.ListSettingChanges(ctx, dbgen.ListSettingChangesParams{StoreID: env.Caller.StoreID, Key: in.Key, BeforeID: in.BeforeID, RowLimit: limit})
			if err != nil {
				return nil, err
			}
			out := &SettingChangeList{Changes: make([]SettingChange, 0, len(rows))}
			for _, row := range rows {
				out.Changes = append(out.Changes, changeView(row.SettingChange, row.ChangedByName))
			}
			return out, nil
		},
	})
}

func dayOrToday(ctx context.Context, env ops.Env, s string) (time.Time, error) {
	if s == "" {
		return BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	}
	d, err := time.Parse(DateLayout, s)
	if err != nil {
		// rule-untested: GetSettingsInput.Date 的 schema format=date 在调用前校验；空值走 BusinessDate
		return time.Time{}, ops.Rule("日期的格式是 YYYY-MM-DD")
	}
	return d, nil
}

func listSettings(ctx context.Context, env ops.Env, day time.Time) (*SettingList, error) {
	rows, err := effectiveRows(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	out := &SettingList{BusinessDate: day.Format(DateLayout), Settings: make([]Setting, 0, len(Catalogue))}
	for _, d := range Catalogue {
		st := Setting{Key: d.Key, Label: d.Label, Hint: d.Hint, Kind: d.Kind, Options: d.Options, Min: d.Min, Max: d.Max, Value: d.Default, Default: d.Default, IsDefault: true, Upcoming: []ScheduledValue{}}
		if row, ok := configured(rows, d.Key); ok {
			st.Value = row.value
			st.IsDefault = false
			st.EffectiveFrom = new(row.effectiveFrom.Format(DateLayout))
		}
		out.Settings = append(out.Settings, st)
	}
	upcoming, err := env.Q.UpcomingSettingChanges(ctx, dbgen.UpcomingSettingChangesParams{StoreID: env.Caller.StoreID, After: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, u := range upcoming {
		for i := range out.Settings {
			if st := &out.Settings[i]; st.Key == u.Key {
				v := ScheduledValue{EffectiveFrom: u.EffectiveFrom.Time.Format(DateLayout), Value: st.Default, IsDefault: true}
				if !u.IsDefault {
					v.Value, v.IsDefault = json.RawMessage(u.Value), false
				}
				st.Upcoming = append(st.Upcoming, v)
			}
		}
	}
	return out, nil
}

func changeSetting(ctx context.Context, env ops.Env, in *ChangeSettingInput) (*SettingChange, error) {
	d, ok := Lookup(in.Key)
	if !ok {
		return nil, ops.Rule("没有这项设置：" + in.Key)
	}
	from, err := time.Parse(DateLayout, in.EffectiveFrom)
	if err != nil {
		// rule-untested: ChangeSettingInput.EffectiveFrom 的 schema format=date 在调用前用 time.DateOnly 校验
		return nil, ops.Rule("生效日期的格式是 YYYY-MM-DD")
	}
	today, err := BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	if from.Before(today) {
		// 修改只影响之后发生的业务（ADR-0002）。
		return nil, ops.Rule("生效日期不能早于今天（" + today.Format(DateLayout) + "）")
	}
	if d.fromTomorrow && !from.After(today) {
		return nil, ops.Rule(d.Label + "最早从明天（" + today.AddDate(0, 0, 1).Format(DateLayout) + "）开始执行")
	}
	value, isDefault := defaultJSON(d), in.Value == nil
	if !isDefault {
		if value, err = json.Marshal(in.Value); err != nil {
			return nil, err
		}
		if err := d.validate(value); err != nil {
			return nil, ops.Rule(err.Error())
		}
	}
	// 改前的值：生效日期那天原本执行的值。同一项设置的修改排队进行，改前值才接得上前一次的改后值。
	if err := env.Q.LockSettingKey(ctx, dbgen.LockSettingKeyParams{StoreID: env.Caller.StoreID, Key: d.Key}); err != nil {
		return nil, err
	}
	rows, err := effectiveRows(ctx, env.Q, env.Caller.StoreID, from)
	if err != nil {
		return nil, err
	}
	old, oldIsDefault := defaultJSON(d), true
	if row, ok := configured(rows, d.Key); ok {
		old, oldIsDefault = row.value, false
	}
	row, err := env.Q.AddSettingChange(ctx, dbgen.AddSettingChangeParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Key: d.Key,
		Value: value, IsDefault: isDefault, OldValue: old, OldIsDefault: oldIsDefault,
		EffectiveFrom: pgDate(from), ChangedBy: env.Caller.StaffID,
	})
	if err != nil {
		return nil, err
	}
	by, err := env.Q.GetStaff(ctx, row.ChangedBy)
	if err != nil {
		return nil, err
	}
	return new(changeView(row, by.Name)), nil
}

func changeView(row dbgen.SettingChange, changedBy string) SettingChange {
	// 已从目录中去掉的设置项仍然显示记录，名称用 key。
	label := row.Key
	if d, ok := Lookup(row.Key); ok {
		label = d.Label
	}
	return SettingChange{
		ID: row.ID, Key: row.Key, Label: label,
		OldValue: json.RawMessage(row.OldValue), OldIsDefault: row.OldIsDefault, Value: json.RawMessage(row.Value), IsDefault: row.IsDefault,
		EffectiveFrom: row.EffectiveFrom.Time.Format(DateLayout),
		ChangedBy:     staff.Ref{ID: row.ChangedBy, Name: changedBy}, ChangedAt: row.ChangedAt.Time,
	}
}
