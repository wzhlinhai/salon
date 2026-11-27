package reminders

// registeredRules 是生产环境注册的全部提醒规则，顺序即管理后台的显示顺序。
var registeredRules = []Rule{closingYesterdayBrief}

// Rules 返回全部已注册提醒规则。
func Rules() []Rule { return registeredRules }

// RuleByKey 按标识查找已注册规则。
func RuleByKey(key string) (Rule, bool) {
	for _, rule := range registeredRules {
		if rule.Key == key {
			return rule, true
		}
	}
	return Rule{}, false
}

// DefaultMode 返回门店没有设置时这条规则使用的档位。
func (r Rule) defaultMode() Mode {
	if r.DefaultMode == "" {
		return ModeObserve
	}
	return r.DefaultMode
}
