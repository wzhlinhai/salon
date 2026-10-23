package money

import (
	"database/sql/driver"
	"encoding/json"
	"regexp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"
)

const changePattern = `^-?(0|[1-9][0-9]{0,7})(\.[0-9]{1,2})?$`

var changeRegexp = regexp.MustCompile(changePattern)

// Change 是精确到分的有符号金额变动，JSON 使用字符串。
type Change struct{ value decimal.Decimal }

// String 返回固定两位小数。
func (c Change) String() string { return c.value.StringFixed(2) }

// MarshalJSON 将变动编码为两位小数的字符串。
func (c Change) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// UnmarshalJSON 读取有符号十进制字符串。
func (c *Change) UnmarshalJSON(data []byte) error { return c.Scan(json.RawMessage(data)) }

// Schema 将变动格式提供给 OpenAPI 和 AI 工具。
func (Change) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Pattern: changePattern}
}

// Value 将精确十进制文本交给数据库驱动。
func (c Change) Value() (driver.Value, error) { return c.String(), nil }

// Scan 读取 NUMERIC 十进制文本，不经过浮点。
func (c *Change) Scan(src any) error { return scanDecimal(&c.value, src, changeRegexp) }

// Sub 返回目标金额与原金额的精确差额。
func (a Amount) Sub(before Amount) Change { return Change{value: a.value.Sub(before.value)} }

// Apply 将有符号变动加到金额上，结果为负或超过金额上限时返回错误。
func (a Amount) Apply(delta Change) (Amount, error) {
	var out Amount
	err := out.Scan(a.value.Add(delta.value).StringFixed(2))
	return out, err
}
