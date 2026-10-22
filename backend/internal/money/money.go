// Package money 提供精确到两位小数的金额和百分比。
package money

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"
)

const amountPattern = `^(0|[1-9][0-9]{0,7})(\.[0-9]{1,2})?$`
const percentPattern = `^((0|[1-9][0-9]?)(\.[0-9]{1,2})?|100(\.0{1,2})?)$`

var (
	amountRegexp  = regexp.MustCompile(amountPattern)
	percentRegexp = regexp.MustCompile(percentPattern)
)

// Amount 是精确到分的非负人民币金额，JSON 使用字符串。
type Amount struct{ value decimal.Decimal }

// String 返回固定两位小数。
func (a Amount) String() string { return a.value.StringFixed(2) }

// MarshalJSON 将数值编码为两位小数的字符串。
func (a Amount) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

// UnmarshalJSON 只接受最多两位小数的非负十进制字符串。
func (a *Amount) UnmarshalJSON(data []byte) error {
	return a.Scan(json.RawMessage(data))
}

// Schema 将金额格式提供给 OpenAPI 和 AI 工具。
func (Amount) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Pattern: amountPattern}
}

// Value 将精确十进制文本交给数据库驱动。
func (a Amount) Value() (driver.Value, error) { return a.String(), nil }

// Scan 读取 PostgreSQL NUMERIC 的十进制文本，不经过浮点。
func (a *Amount) Scan(src any) error {
	return scanDecimal(&a.value, src, amountRegexp)
}

// Percent 是零到一百之间的百分比，JSON 使用字符串。
type Percent struct{ value decimal.Decimal }

// String 返回固定两位小数。
func (a Percent) String() string { return a.value.StringFixed(2) }

// MarshalJSON 将数值编码为两位小数的字符串。
func (a Percent) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

// UnmarshalJSON 只接受最多两位小数的非负十进制字符串。
func (a *Percent) UnmarshalJSON(data []byte) error {
	return a.Scan(json.RawMessage(data))
}

// Schema 将百分比格式提供给 OpenAPI 和 AI 工具。
func (Percent) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Pattern: percentPattern}
}

// Value 将精确十进制文本交给数据库驱动。
func (a Percent) Value() (driver.Value, error) { return a.String(), nil }

// Scan 读取 PostgreSQL NUMERIC 的十进制文本，不经过浮点。
func (a *Percent) Scan(src any) error {
	return scanDecimal(&a.value, src, percentRegexp)
}

func scanDecimal(value *decimal.Decimal, src any, pattern *regexp.Regexp) error {
	var s string
	switch v := src.(type) {
	case json.RawMessage:
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("不能将 %T 读取为十进制数值", src)
	}
	if !pattern.MatchString(s) {
		return fmt.Errorf("无效的十进制数值: %q", s)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return err
	}
	*value = d
	return nil
}

// Add 精确相加，超过金额上限 99999999.99 时返回错误。
func (a Amount) Add(b Amount) (Amount, error) {
	var out Amount
	err := out.Scan(a.value.Add(b.value).StringFixed(2))
	return out, err
}
