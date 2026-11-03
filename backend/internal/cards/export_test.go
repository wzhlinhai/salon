package cards

// OpenCardForTest 在测试持有的事务里执行真实开卡流程，供并发测试控制提交时刻。
var OpenCardForTest = openCard
