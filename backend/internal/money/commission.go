package money

import "github.com/shopspring/decimal"

// Prorate 按实收份额分配金额并四舍五入到分，分母为零时返回零。
func (a Amount) Prorate(numerator Amount, denominator Total) Amount {
	if denominator.value.IsZero() {
		return Amount{}
	}
	return Amount{value: a.value.Mul(numerator.value).Div(denominator.value).Round(2)}
}

// Negate 返回反向金额变动。
func (c Change) Negate() Change { return Change{value: c.value.Neg()} }

// SignedTotal 是不受单笔金额上限限制的有符号累计金额。
type SignedTotal struct{ value decimal.Decimal }

// Add 累加一笔有符号金额。
func (s SignedTotal) Add(c Change) SignedTotal { return SignedTotal{value: s.value.Add(c.value)} }

// Format 返回累计净额的固定两位小数显示文本。
func (s SignedTotal) Format() string { return s.value.StringFixed(2) }

// ChangeFromFen 将整数分转为精确的有符号变动，供累计与分段计算。
func ChangeFromFen(fen int64) Change { return Change{value: decimal.NewFromInt(fen).Shift(-2)} }

// Fen 返回变动对应的有符号整数分。
func (c Change) Fen() int64 { return c.value.Shift(2).IntPart() }

// Share 按比例计算有符号金额并四舍五入到分。
func (c Change) Share(percent Percent) Change {
	return Change{value: c.value.Mul(percent.value).Div(decimal.NewFromInt(100)).Round(2)}
}
