package money

import "github.com/shopspring/decimal"

// Fen 返回金额对应的整数分，用于守恒分摊。
func (a Amount) Fen() int64 { return a.value.Shift(2).IntPart() }

// FromFen 将非负整数分转为单笔金额，越过金额范围时返回错误。
func FromFen(fen int64) (Amount, error) {
	var out Amount
	err := out.Scan(decimal.NewFromInt(fen).Shift(-2).StringFixed(2))
	return out, err
}
