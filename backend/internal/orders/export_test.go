package orders

// OpenOrderAtForTest 让接口测试以固定时刻调用同一开单处理函数。
var OpenOrderAtForTest = openOrderAt

// CheckEditForTest 让测试以固定时刻检查改单的营业日窗口和本人范围。
var CheckEditForTest = checkEdit

// CheckoutOrdersAtForTest 让接口测试以固定时刻调用同一结账处理函数。
var CheckoutOrdersAtForTest = checkoutOrdersAt

// VoidOrderAtForTest 让撤单测试使用固定营业时刻。
var VoidOrderAtForTest = voidOrderAt

// ViewsForTest 让测试比较单据批量子项查询次数。
var ViewsForTest = views
