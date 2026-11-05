// Package fakeprovider 提供仅供测试使用的内存团购适配器，不用于真实核销。
package fakeprovider

import (
	"context"
	"sync"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
)

// Reply 是一次可编程的平台响应，Apply 表示错误前平台实际已完成动作。
type Reply struct {
	Result groupbuy.CouponResult
	Err    error
	Apply  bool
}

// Fake 在内存中按券保存状态、按动作和键去重，脚本响应可模拟超时。
type Fake struct {
	mu       sync.Mutex
	Platform string
	Now      time.Time
	Replies  map[string][]Reply
	States   map[string]groupbuy.CouponResult
	seen     map[string]groupbuy.CouponResult
	calls    map[string]int
	// Before 在调用时执行，可验证外部调用期间无数据库行锁。
	Before func(string, groupbuy.CouponRequest)
}

// New 创建由测试指定平台时刻的适配器。
func New(platform string, now time.Time) *Fake {
	return &Fake{Platform: platform, Now: now, Replies: map[string][]Reply{}, States: map[string]groupbuy.CouponResult{}, seen: map[string]groupbuy.CouponResult{}, calls: map[string]int{}}
}

// Count 返回某动作实际收到的请求数，包括重复键请求。
func (f *Fake) Count(action string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[action] }

func (f *Fake) call(action string, in groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	if f.Before != nil {
		f.Before(action, in)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[action]++
	scope := in.TenantID.String() + "/" + in.StoreID.String() + "/"
	stateKey := scope + in.CouponCode
	actionKey := scope + action + ":" + in.Key
	if replies := f.Replies[action]; len(replies) > 0 {
		r := replies[0]
		f.Replies[action] = replies[1:]
		if r.Apply {
			f.States[stateKey] = r.Result
			f.seen[actionKey] = r.Result
		}
		return r.Result, r.Err
	}
	if result, ok := f.seen[actionKey]; ok && (action == "redeem" || action == "revoke") {
		return result, nil
	}
	result, ok := f.States[stateKey]
	if !ok {
		result = groupbuy.CouponResult{Status: "unused", Platform: f.Platform}
	}
	if action == "redeem" && result.Status == "unused" {
		result.Status = "redeemed"
		instant := f.Now
		result.RedeemedAt = &instant
	}
	if action == "revoke" && result.Status == "redeemed" {
		result.Status = "revoked"
	}
	f.States[stateKey] = result
	f.seen[actionKey] = result
	return result, nil
}

// Verify 返回内存券状态。
func (f *Fake) Verify(_ context.Context, in groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return f.call("verify", in)
}

// Redeem 用幂等键核销内存券。
func (f *Fake) Redeem(_ context.Context, in groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return f.call("redeem", in)
}

// Revoke 用撤销幂等键撤销内存券。
func (f *Fake) Revoke(_ context.Context, in groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return f.call("revoke", in)
}

// Query 返回平台当前状态，可用脚本模拟仍未知。
func (f *Fake) Query(_ context.Context, in groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return f.call("query", in)
}
