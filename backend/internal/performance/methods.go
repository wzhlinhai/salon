package performance

import "github.com/wzhlinhai/salon/backend/internal/settings"

type paymentMethod struct {
	name, ratioKey, group string
}

// 非 count 组按表序参与项目分配和补分；count 组只用于耗卡业绩。
var paymentMethods = []paymentMethod{
	{"cash", settings.KeyPerformanceRatioCash, "cash"},
	{"unionpay", settings.KeyPerformanceRatioUnionpay, "cash"},
	{"wechat", settings.KeyPerformanceRatioWechat, "cash"},
	{"alipay", settings.KeyPerformanceRatioAlipay, "cash"},
	{"groupbuy", settings.KeyPerformanceRatioGroupbuy, ""},
	{"debt", settings.KeyPerformanceRatioDebt, "cash"},
	{"red_packet", settings.KeyPerformanceRatioRedPacket, ""},
	{"card_principal", settings.KeyPerformanceRatioCardPrincipal, "card"},
	{"card_bonus", settings.KeyPerformanceRatioCardBonus, "card"},
	{"count_paid", settings.KeyPerformanceRatioCountPaid, "count"},
	{"count_gift", settings.KeyPerformanceRatioCountGift, "count"},
}

func allocationMethods() []string {
	out := make([]string, 0, len(paymentMethods))
	for _, m := range paymentMethods {
		if m.group != "count" {
			out = append(out, m.name)
		}
	}
	return out
}
