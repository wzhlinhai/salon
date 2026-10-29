package orders

import (
	"context"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

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

// RegisterVoidWindowAtForTest 让 HTTP 测试使用固定时刻执行窗口和撤单操作。
func RegisterVoidWindowAtForTest(r *ops.Registry, now func() time.Time) {
	ops.Register(r, ops.Operation[OpenVoidWindowInput, VoidWindow]{Name: "test_open_void_window", Kind: ops.Write, Permission: perm.OrderVoid, AI: ops.AINone, Handle: func(ctx context.Context, env ops.Env, in *OpenVoidWindowInput) (*VoidWindow, error) {
		return openVoidWindowAt(ctx, env, in, now())
	}})
	ops.Register(r, ops.Operation[VoidOrderInput, Order]{Name: "test_void_order", Kind: ops.Write, Permission: perm.OrderVoid, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, in *VoidOrderInput) (*Order, error) {
		return voidOrderAt(ctx, env, in, now())
	}})
}
