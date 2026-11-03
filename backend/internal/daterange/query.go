package daterange

import (
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Query 是一次按营业日范围查询的条件。
type Query struct {
	From, To time.Time  // 已校验：From ≤ To，跨度 ≤ 92 天
	StaffID  *uuid.UUID // 本人范围与请求求交后的员工条件
	Empty    bool       // 本人范围与请求的员工不同，结果必为空
	Limit    int32      // 0 → 50
}

// Parse 校验营业日范围和上限，并按权限范围求交员工条件。
func Parse(caller ops.Caller, p ops.Permission, dateFrom, dateTo string, staffID *uuid.UUID, limit int32) (Query, error) {
	from, fromErr := time.Parse(settings.DateLayout, dateFrom)
	to, toErr := time.Parse(settings.DateLayout, dateTo)
	if fromErr != nil || toErr != nil {
		// rule-untested: 三个列表的日期在调用前由 schema format=date 校验；Parse 单元测试直接覆盖无效日期
		return Query{}, ops.Rule("请填写有效的营业日")
	}
	if from.After(to) {
		return Query{}, ops.Rule("开始营业日不能晚于结束营业日")
	}
	if to.Sub(from) > 92*24*time.Hour {
		return Query{}, ops.Rule("查询范围最多 92 天")
	}
	q := Query{From: from, To: to, StaffID: staffID, Limit: limit}
	_, self := caller.ScopeFilter(p)
	if self != nil {
		q.StaffID = self
		q.Empty = staffID != nil && *staffID != *self
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	return q, nil
}
