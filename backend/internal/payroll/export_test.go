package payroll

import (
	"context"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// LockPayrollMonthForTest 在测试持有的事务里执行真实锁月流程，供并发测试控制提交时刻。
func LockPayrollMonthForTest(ctx context.Context, env ops.Env, in *MonthInput) (*Payroll, error) {
	return lockPayrollMonth(ctx, env, in)
}
