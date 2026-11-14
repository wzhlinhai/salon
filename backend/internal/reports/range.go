package reports

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func dateRange(caller ops.Caller, permission ops.Permission, from, to string) (daterange.Query, error) {
	return daterange.Parse(caller, permission, from, to, nil, 0)
}

func staffDateRange(caller ops.Caller, from, to string, staffID *uuid.UUID) (daterange.Query, error) {
	return daterange.Parse(caller, perm.PerformanceView, from, to, staffID, 0)
}

func monthRange(from, to string) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01", from)
	if err != nil {
		// rule-untested: 月报的月份格式由解析函数覆盖，HTTP schema 只校验字符串
		return time.Time{}, time.Time{}, ops.Rule("请填写有效的月份")
	}
	end, err := time.Parse("2006-01", to)
	if err != nil {
		return time.Time{}, time.Time{}, ops.Rule("请填写有效的月份")
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, ops.Rule("开始月份不能晚于结束月份")
	}
	months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
	if months > 12 {
		return time.Time{}, time.Time{}, ops.Rule("查询范围最多 12 个月")
	}
	first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, settings.Location)
	last := time.Date(end.Year(), end.Month()+1, 1, 0, 0, 0, 0, settings.Location).AddDate(0, 0, -1)
	return first, last, nil
}

func pgDate(day time.Time) pgtype.Date { return pgtype.Date{Time: day, Valid: true} }
