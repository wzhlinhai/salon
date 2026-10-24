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
	// KindChoice：从选项中选择字符串。
	KindChoice Kind = "choice"
	// KindInteger：非负整数。
	KindInteger Kind = "integer"
	// KindTimeRange：一天中的时段，{"open": "HH:MM", "close": "HH:MM"}。
	KindTimeRange Kind = "time_range"
)

// TimeRange 是一天中的时段。
type TimeRange struct {
	Open  string `json:"open"`
	Close string `json:"close"`
}

// ChoiceOption 是设置的一个可选做法。
type ChoiceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Def 描述一项门店设置。
type Def struct {
	// Key 是设置项的标识，保存在修改记录和快照里，不能改名。
	Key string
	// Label 和 Hint 是面向人的名称和说明。
	Label   string
	Hint    string
	Kind    Kind
	Options []ChoiceOption
	Min     int
	Max     int
	// Default 是系统默认做法，门店没有设置时使用。
	Default any
	// check 校验 JSON 值，返回面向人的错误说明。
	check func(raw json.RawMessage) error
	// fromTomorrow：不能当天生效。用于划分营业日的设置，当天改会改变今天已经发生的业务属于哪一天。
	fromTomorrow bool
}

// 设置项的 Key。
const (
	KeyCardDeductionOrder    = "card_deduction_order"
	KeyCardCountOrder        = "card_count_order"
	KeyOthersCardPayment     = "others_card_payment"
	KeyMemberPriceRule       = "member_price_rule"
	KeyPointsPerRechargeYuan = "points_per_recharge_yuan"
	KeyBusinessHours         = "business_hours"
	KeyBusinessDayEnd        = "business_day_end"
)

// Catalogue 是全部门店设置，顺序即设置页面的显示顺序。后续模块的业务规则在这里添加。
var Catalogue = []Def{

	{
		Key: KeyMemberPriceRule, Label: "会员价取价", Kind: KindChoice,
		Hint: "持卡顾客做有级别价的项目时，会员价和级别价取哪个", Default: "member_first",
		Options: []ChoiceOption{{Value: "member_first", Label: "会员价优先"}, {Value: "lowest", Label: "取较低价"}},
	},
	{
		Key: KeyPointsPerRechargeYuan, Label: "充值积分", Kind: KindInteger, Min: 0, Max: 100000,
		Hint: "每充值多少元本金积 1 分，0 为不积分，不足一分的部分不计", Default: 0,
	},
	{Key: KeyCardDeductionOrder, Label: "扣款顺序", Hint: "用卡余额付钱时先扣本金还是先扣赠送金，先扣的不够再扣另一部分", Kind: KindChoice, Default: "principal_first", Options: []ChoiceOption{{Value: "principal_first", Label: "先扣本金"}, {Value: "bonus_first", Label: "先扣赠送金"}}},
	{Key: KeyOthersCardPayment, Label: "用别人的卡", Hint: "顾客能否用别人（例如家人）的卡余额付自己的单", Kind: KindChoice, Default: "not_allowed", Options: []ChoiceOption{{Value: "not_allowed", Label: "不允许"}, {Value: "allowed", Label: "允许"}}},
	{Key: KeyCardCountOrder, Label: "扣次顺序", Hint: "扣次时先扣购买次数还是赠送次数，先扣的用完再扣另一种", Kind: KindChoice, Default: "paid_first", Options: []ChoiceOption{{Value: "paid_first", Label: "先扣购买次数"}, {Value: "gift_first", Label: "先扣赠送次数"}}},
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
		fromTomorrow: true,
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

// validate 按值形式校验，选项和范围以目录定义为准。
func (d Def) validate(raw json.RawMessage) error {
	switch d.Kind {
	case KindChoice:
		var value string
		if err := strict(raw, &value); err != nil {
			return fmt.Errorf("%s须选择一种做法", d.Label)
		}
		for _, option := range d.Options {
			if value == option.Value {
				return nil
			}
		}
		return fmt.Errorf("%s的做法无效", d.Label)
	case KindInteger:
		var value int
		if err := strict(raw, &value); err != nil || value < 0 || value < d.Min || value > d.Max {
			return fmt.Errorf("%s须为 %d 到 %d 的整数", d.Label, d.Min, d.Max)
		}
		return nil
	default:
		return d.check(raw)
	}
}
