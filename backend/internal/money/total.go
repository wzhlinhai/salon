package money

import (
	"regexp"

	"github.com/shopspring/decimal"
)

var totalRegexp = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`)

// Total 是不受单笔金额上限限制的非负累计金额。
type Total struct{ value decimal.Decimal }

// Scan 读取 PostgreSQL 汇总金额的十进制文本，不经过浮点。
func (a *Total) Scan(src any) error { return scanDecimal(&a.value, src, totalRegexp) }

// Cmp 精确比较累计金额和单笔金额，依次返回 -1、0 或 1。
func (a Total) Cmp(b Amount) int { return a.value.Cmp(b.value) }

// AddProduct 将单价乘以非负数量后累加，数量为负时 panic。
func (a Total) AddProduct(price Amount, count int64) Total {
	if count < 0 {
		panic("money: AddProduct count must be nonnegative")
	}
	return Total{value: a.value.Add(price.value.Mul(decimal.NewFromInt(count)))}
}

// Format 返回累计金额的固定两位小数显示文本。
func (a Total) Format() string { return a.value.StringFixed(2) }
