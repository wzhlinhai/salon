package orders

// OpenOrderAtForTest 让接口测试以固定时刻调用同一开单处理函数。
var OpenOrderAtForTest = openOrderAt

// CheckEditForTest 让测试以固定时刻检查改单的营业日窗口和本人范围。
var CheckEditForTest = checkEdit

// CheckoutOrdersAtForTest 让接口测试以固定时刻调用同一结账处理函数。
var CheckoutOrdersAtForTest = checkoutOrdersAt
