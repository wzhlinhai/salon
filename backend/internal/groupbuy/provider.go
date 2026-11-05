// Package groupbuy 定义团购平台适配协议和人工核销操作。
package groupbuy

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrUnknown 表示网络或超时导致平台结果无法确定，必须先查询。
var ErrUnknown = errors.New("groupbuy: result unknown")

// CouponRequest 是每个适配动作的券信息和幂等键。
type CouponRequest struct {
	// TenantID 和 StoreID 由操作层填入，适配器必须按此范围隔离幂等键。
	TenantID   uuid.UUID `json:"-"`
	StoreID    uuid.UUID `json:"-"`
	Platform   string    `json:"platform" minLength:"1" maxLength:"100"`
	CouponCode string    `json:"coupon_code" minLength:"1" maxLength:"500"`
	Key        string    `json:"key" minLength:"1" maxLength:"200"`
}

// CouponResult 是平台统一券状态及平台核销时刻。
type CouponResult struct {
	Status     string     `json:"status" enum:"unused,redeemed,revoked,invalid"`
	Platform   string     `json:"platform"`
	RedeemedAt *time.Time `json:"redeemed_at"`
}

// Provider 是验券、核销、撤销和查询的统一协议；实现必须保证同键动作幂等。
type Provider interface {
	Verify(context.Context, CouponRequest) (CouponResult, error)
	Redeem(context.Context, CouponRequest) (CouponResult, error)
	Revoke(context.Context, CouponRequest) (CouponResult, error)
	Query(context.Context, CouponRequest) (CouponResult, error)
}

// Providers 按平台保存可替换的适配实现，零值可用。
type Providers struct {
	mu      sync.RWMutex
	entries map[string]Provider
}

// Register 注册或替换指定平台的实现。
func (p *Providers) Register(platform string, provider Provider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = map[string]Provider{}
	}
	p.entries[platform] = provider
}

// Get 取得平台适配实现；未配置时返回 nil。
func (p *Providers) Get(platform string) Provider {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.entries[platform]
}
