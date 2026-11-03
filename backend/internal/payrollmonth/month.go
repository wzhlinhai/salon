// Package payrollmonth 统一结算月、开店月下限与锁定流程。
package payrollmonth

import (
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// Month 是以 UTC 月首表示、可作映射键的结算月。
type Month struct{ first time.Time }

// Parse 解析 YYYY-MM 格式的结算月。
func Parse(s string) (Month, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		// rule-untested: 所有月份输入均由 schema pattern 在调用前校验
		return Month{}, ops.Rule("结算月的格式是 YYYY-MM")
	}
	return Of(t), nil
}

// Of 返回营业日所属的结算月。
func Of(day time.Time) Month {
	return Month{time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)}
}

// String 返回 YYYY-MM 格式的结算月。
func (m Month) String() string { return m.first.Format("2006-01") }

// First 返回结算月首日。
func (m Month) First() time.Time { return m.first }

// Last 返回结算月末日。
func (m Month) Last() time.Time { return m.first.AddDate(0, 1, -1) }

// Prev 返回前一个结算月。
func (m Month) Prev() Month { return Of(m.first.AddDate(0, -1, 0)) }

// Before 判断结算月是否早于另一结算月。
func (m Month) Before(other Month) bool { return m.first.Before(other.first) }
