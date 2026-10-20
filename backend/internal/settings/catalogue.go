package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// Kind 是设置值的形式，管理后台按它显示编辑控件。
type Kind string

const (
	// KindTime：一天中的时刻，"HH:MM"。
	KindTime Kind = "time"
	// KindTimeRange：一天中的时段，{"open": "HH:MM", "close": "HH:MM"}。
	KindTimeRange Kind = "time_range"
)

// TimeRange 是一天中的时段。
type TimeRange struct {
	Open  string `json:"open"`
	Close string `json:"close"`
}

// Def 描述一项门店设置。
type Def struct {
	Key   string
	Label string
	Hint  string
	Kind  Kind
	// Default 是系统默认做法，门店没有设置时使用。
	Default any
	// check 校验 JSON 值，返回面向人的错误说明。
	check func(raw json.RawMessage) error
}

const (
	KeyBusinessHours  = "business_hours"
	KeyBusinessDayEnd = "business_day_end"
)

// Catalogue 是全部门店设置，顺序即设置页面的显示顺序。后续模块的业务规则在这里添加。
var Catalogue = []Def{
	{
		Key: KeyBusinessHours, Label: "营业时段", Kind: KindTimeRange,
		Hint:    "每天开门和关门的时间，预约等按它限制",
		Default: TimeRange{Open: "09:00", Close: "22:00"},
		check: func(raw json.RawMessage) error {
			var r TimeRange
			if err := strict(raw, &r); err != nil || !isClock(r.Open) || !isClock(r.Close) {
				return fmt.Errorf("营业时段的格式是 {\"open\": \"HH:MM\", \"close\": \"HH:MM\"}")
			}
			if r.Open >= r.Close {
				return fmt.Errorf("开门时间要早于关门时间")
			}
			return nil
		},
	},
	{
		Key: KeyBusinessDayEnd, Label: "营业日结束时间", Kind: KindTime,
		Hint:    "次日几点之前发生的业务算前一天。草稿过期、日结都按它划分营业日",
		Default: "00:00",
		check: func(raw json.RawMessage) error {
			var s string
			if err := strict(raw, &s); err != nil || !isClock(s) {
				return fmt.Errorf("营业日结束时间的格式是 \"HH:MM\"")
			}
			if s >= "12:00" {
				return fmt.Errorf("营业日结束时间要在 00:00 到 11:59 之间")
			}
			return nil
		},
	},
}

// Lookup 返回设置项 key 的定义。
func Lookup(key string) (Def, bool) {
	for _, d := range Catalogue {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

var clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func isClock(s string) bool { return clockPattern.MatchString(s) }

// strict 解码 raw 到 v，不允许多余的字段。
func strict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
