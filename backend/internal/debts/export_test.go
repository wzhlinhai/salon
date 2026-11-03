package debts

// RepayDebtForTest 在测试持有的事务里执行真实还款流程，供并发测试控制提交时刻。
var RepayDebtForTest = repayDebt
