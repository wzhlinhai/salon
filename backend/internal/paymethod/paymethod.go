// Package paymethod 是收款方式、现场收款划分和显示名称的唯一来源。
package paymethod

// Code 是一种收款方式。
type Code string

const (
	Cash          Code = "cash"
	Unionpay      Code = "unionpay"
	Wechat        Code = "wechat"
	Alipay        Code = "alipay"
	CardPrincipal Code = "card_principal"
	CardBonus     Code = "card_bonus"
	Count         Code = "count"
	RedPacket     Code = "red_packet"
	Debt          Code = "debt"
	Groupbuy      Code = "groupbuy"
)

// Method 是一种收款方式的代码、名称以及是否属于现场收款。
type Method struct {
	Code   Code
	Label  string
	OnSite bool
}

// All 是全部收款方式。新增方式（例如 P0-24 的团购）在这里登记；只有声明 OnSite 的才进入现场收款，其余自动列为非现场。
var All = []Method{
	{Cash, "现金", true},
	{Unionpay, "银联", true},
	{Wechat, "微信", true},
	{Alipay, "支付宝", true},
	{CardPrincipal, "卡本金", false},
	{CardBonus, "卡赠送金", false},
	{Count, "扣次", false},
	{RedPacket, "红包", false},
	{Debt, "挂账", false},
	{Groupbuy, "团购", false},
}

// OnSite 返回现场收款方式。
func OnSite() []Method {
	out := []Method{}
	for _, m := range All {
		if m.OnSite {
			out = append(out, m)
		}
	}
	return out
}

// NonSite 返回非现场收款方式。
func NonSite() []Method {
	out := []Method{}
	for _, m := range All {
		if !m.OnSite {
			out = append(out, m)
		}
	}
	return out
}

// Label 返回收款方式的中文名称。
func Label(code string) string {
	for _, m := range All {
		if string(m.Code) == code {
			return m.Label
		}
	}
	return code
}
