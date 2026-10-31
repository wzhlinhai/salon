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
	KeyCardDeductionOrder            = "card_deduction_order"
	KeyCardCountOrder                = "card_count_order"
	KeyCardSelection                 = "card_selection"
	KeyOthersCardPayment             = "others_card_payment"
	KeyMemberPriceRule               = "member_price_rule"
	KeyPointsPerRechargeYuan         = "points_per_recharge_yuan"
	KeyGuestCountSplit               = "guest_count_split"
	KeyPerformanceSplit              = "performance_split"
	KeyPerformanceRatioCash          = "performance_ratio_cash"
	KeyPerformanceRatioUnionpay      = "performance_ratio_unionpay"
	KeyPerformanceRatioWechat        = "performance_ratio_wechat"
	KeyPerformanceRatioAlipay        = "performance_ratio_alipay"
	KeyPerformanceRatioDebt          = "performance_ratio_debt"
	KeyPerformanceRatioCardPrincipal = "performance_ratio_card_principal"
	KeyPerformanceRatioCardBonus     = "performance_ratio_card_bonus"
	KeyPerformanceRatioRedPacket     = "performance_ratio_red_packet"
	KeyPerformanceRatioCountPaid     = "performance_ratio_count_paid"
	KeyPerformanceRatioCountGift     = "performance_ratio_count_gift"
	KeyCardRefundBonus               = "card_refund_bonus"
	KeyCardRefundFeePercent          = "card_refund_fee_percent"
	KeyVoidWindowMinutes             = "void_window_minutes"
	KeyBusinessHours                 = "business_hours"
	KeyBusinessDayEnd                = "business_day_end"
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
	{Key: KeyCardSelection, Label: "选卡规则", Hint: "结账只说用卡余额付、不指定哪张卡时怎么挑卡：手动选卡必须指定；按折扣率先扣这单实收最低的卡；按余额先扣余额少的卡，一张不够接着扣下一张", Kind: KindChoice, Default: "manual", Options: []ChoiceOption{{Value: "manual", Label: "手动选卡"}, {Value: "by_discount", Label: "按折扣率"}, {Value: "by_balance", Label: "按余额"}}},
	{Key: KeyGuestCountSplit, Label: "客数归属", Kind: KindChoice, Default: "first_slot", Options: []ChoiceOption{{Value: "first_slot", Label: "第一个岗位的员工"}, {Value: "equal", Label: "单上员工均分"}}, Hint: "每张结账单的客次记给谁"},
	{Key: KeyPerformanceSplit, Label: "多人分业绩", Kind: KindChoice, Default: "by_position", Options: []ChoiceOption{{Value: "by_position", Label: "按岗位比例"}, {Value: "equal", Label: "均分"}}, Hint: "一个项目几个人做时业绩怎么分；项目没设岗位比例时均分"},
	{Key: KeyPerformanceRatioCash, Label: "现金业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioUnionpay, Label: "银联业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioWechat, Label: "微信业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioAlipay, Label: "支付宝业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioDebt, Label: "挂账业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioCardPrincipal, Label: "卡余额本金业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioCardBonus, Label: "卡余额赠送金业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 0, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioRedPacket, Label: "红包业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 0, Hint: "这种支付方式付的钱按百分之几计入业绩"},
	{Key: KeyPerformanceRatioCountPaid, Label: "购买次数耗卡业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 100, Hint: "扣次时按单次金额的百分之几计入耗卡业绩"},
	{Key: KeyPerformanceRatioCountGift, Label: "赠送次数耗卡业绩比例", Kind: KindInteger, Min: 0, Max: 100, Default: 0, Hint: "扣次时按单次金额的百分之几计入耗卡业绩"},
	{
		Key: KeyCardRefundBonus, Label: "退卡赠送金", Kind: KindChoice, Default: "forfeit",
		Options: []ChoiceOption{{Value: "forfeit", Label: "不退、作废"}, {Value: "deduct_used", Label: "扣回已用掉的赠送金"}},
		Hint:    "退卡时剩余赠送金都作废；选扣回时，退款里再减去这张卡结账时用掉的赠送金",
	},
	{
		Key: KeyCardRefundFeePercent, Label: "退卡手续费", Kind: KindInteger, Min: 0, Max: 100, Default: 0,
		Hint: "按退款金额的百分比收取，0 为不收",
	},
	{
		Key: KeyVoidWindowMinutes, Label: "撤单授权窗口", Kind: KindInteger, Min: 0, Max: 60,
		Hint: "开启后，撤单前要本人重新输入登录密码开一个窗口，窗口内只有本人能撤，过期要重开；单位分钟，0 为关闭", Default: 0,
	},
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
