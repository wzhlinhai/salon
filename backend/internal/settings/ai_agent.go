package settings

// 门店 AI 运行的三项设置（ADR-0015）。放在单独文件里，原有设置项的顺序不变。
const (
	// KeyAICouponLinkWaitMinutes 是团购券核销后多久检查是否已关联单据。
	KeyAICouponLinkWaitMinutes = "ai_coupon_link_wait_minutes"
	// KeyAIHandoverRemindMinutes 是交办后多久再提醒、再过同样时间改交。
	KeyAIHandoverRemindMinutes = "ai_handover_remind_minutes"
	// KeyAIHandoverManagerMarginMinutes 是交办离时限不到多少分钟时直接交店长。
	KeyAIHandoverManagerMarginMinutes = "ai_handover_manager_margin_minutes"
)

var aiAgentCatalogue = []Def{
	{
		Group: GroupAI,
		Key:   KeyAICouponLinkWaitMinutes, Label: "团购券核销后多久查关联", Kind: KindInteger, Min: 5, Max: 240, Default: 30,
		Hint: "团购券核销后，过多少分钟还没关联单据就提醒核销的人；单位分钟",
	},
	{
		Group: GroupAI,
		Key:   KeyAIHandoverRemindMinutes, Label: "交办多久没处理再提醒", Kind: KindInteger, Min: 5, Max: 240, Default: 30,
		Hint: "交办给员工后，过多少分钟还没处理就再提醒一次；再过同样时间改交当班同岗位的人；单位分钟",
	},
	{
		Group: GroupAI,
		Key:   KeyAIHandoverManagerMarginMinutes, Label: "交办离时限多久交店长", Kind: KindInteger, Min: 0, Max: 120, Default: 15,
		Hint: "交办的时限还剩不到多少分钟时，直接改交当班店长；单位分钟，0 为不提前交店长",
	},
}

func init() {
	Catalogue = append(Catalogue, aiAgentCatalogue...)
}

// AICouponLinkWaitMinutes 返回团购券核销后过多少分钟检查是否已关联单据。
func (r Rules) AICouponLinkWaitMinutes() int {
	return r.integer(KeyAICouponLinkWaitMinutes)
}

// AIHandoverRemindMinutes 返回交办后过多少分钟再提醒、再过同样时间改交。
func (r Rules) AIHandoverRemindMinutes() int {
	return r.integer(KeyAIHandoverRemindMinutes)
}

// AIHandoverManagerMarginMinutes 返回交办离时限不到多少分钟时直接交店长。
func (r Rules) AIHandoverManagerMarginMinutes() int {
	return r.integer(KeyAIHandoverManagerMarginMinutes)
}
