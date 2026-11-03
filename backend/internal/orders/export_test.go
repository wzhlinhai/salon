package orders

// CheckEditForTest 让测试以固定时刻检查改单的营业日窗口和本人范围。
var CheckEditForTest = checkEdit

// ViewsForTest 让测试比较单据批量子项查询次数。
var ViewsForTest = views

// CheckoutOrdersForTest 在测试持有的事务里执行真实结账流程，供并发测试控制提交时刻。
var CheckoutOrdersForTest = checkoutOrders

// VoidOrderForTest 在测试持有的事务里执行真实撤单流程，供并发测试控制提交时刻。
var VoidOrderForTest = voidOrder
