package money

import (
	"regexp"

	"github.com/shopspring/decimal"
)

var totalRegexp = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`)

// Total 是不受单笔金额上限限制的非负累计金额，仅用于读取汇总。
type Total struct{ value decimal.Decimal }

// Scan 读取 PostgreSQL 汇总金额的十进制文本，不经过浮点。
func (a *Total) Scan(src any) error { return scanDecimal(&a.value, src, totalRegexp) }

// Cmp 精确比较累计金额和单笔金额，依次返回 -1、0 或 1。
func (a Total) Cmp(b Amount) int { return a.value.Cmp(b.value) }
