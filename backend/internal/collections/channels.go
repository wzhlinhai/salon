// Package collections 统一单据收款的通道、状态和事件。
package collections

import (
	"context"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Result 是通道返回的统一状态及外部凭据。
type Result struct {
	Status      string
	ExternalRef *string
}

// Summary 是一个营业日的通道收款，按当前状态汇总金额和笔数。
type Summary struct {
	BusinessDate string
	Channel      string
	Amounts      map[string]string
	Counts       map[string]int
}

// Channel 是收款通道的发起、查询、撤销、退款和对账接口。
type Channel interface {
	Initiate(context.Context, ops.Env, dbgen.Collection) (Result, error)
	Query(context.Context, ops.Env, dbgen.Collection) (Result, error)
	Cancel(context.Context, ops.Env, dbgen.Collection) error
	Refund(context.Context, ops.Env, dbgen.Collection) error
	Reconcile(context.Context, ops.Env, time.Time) (Summary, error)
}

// Registration 是一个通道及其结账方式和设置声明。
type Registration struct {
	Code, Name, PaymentMethod string
	Channel                   Channel
}

var registry = map[string]Registration{}
var methods = map[string]string{}

// RegisterChannel 在启动时注册通道，同时声明默认启用的门店设置；重复代码或支付方式会启动失败。
func RegisterChannel(r Registration) {
	if r.Code == "" || r.Channel == nil {
		panic("collections: invalid channel")
	}
	if r.PaymentMethod != "" {
		if _, exists := methods[r.PaymentMethod]; exists {
			panic("collections: duplicate payment method")
		}
	}
	settings.Declare(settings.Def{Key: "collection_channel_" + r.Code, Label: r.Name, Kind: settings.KindChoice, Default: "on", Options: []settings.ChoiceOption{{Value: "on", Label: "启用"}, {Value: "off", Label: "停用"}}})
	registry[r.Code] = r
	if r.PaymentMethod != "" {
		methods[r.PaymentMethod] = r.Code
	}
}

// Lookup 按代码取得通道。
func Lookup(code string) (Registration, error) {
	r, ok := registry[code]
	if !ok {
		return Registration{}, ops.Rule("未知收款通道：" + code)
	}
	return r, nil
}

// ForPayment 查找结账支付方式对应的通道；没有注册的方式由原支付流程处理。
func ForPayment(method string) (string, bool) { code, ok := methods[method]; return code, ok }

type registerChannel struct{ code string }

func (registerChannel) Initiate(_ context.Context, _ ops.Env, row dbgen.Collection) (Result, error) {
	return Result{Status: "succeeded", ExternalRef: row.ExternalRef}, nil
}
func (registerChannel) Query(_ context.Context, _ ops.Env, row dbgen.Collection) (Result, error) {
	return Result{Status: row.Status, ExternalRef: row.ExternalRef}, nil
}
func (registerChannel) Cancel(context.Context, ops.Env, dbgen.Collection) error { return nil }
func (registerChannel) Refund(context.Context, ops.Env, dbgen.Collection) error { return nil }
func (c registerChannel) Reconcile(ctx context.Context, env ops.Env, day time.Time) (Summary, error) {
	return reconcile(ctx, env, c.code, day)
}

func init() {
	RegisterChannel(Registration{Code: "cash", Name: "现金", PaymentMethod: "cash", Channel: registerChannel{code: "cash"}})
	RegisterChannel(Registration{Code: "unionpay_register", Name: "银联登记", PaymentMethod: "unionpay", Channel: registerChannel{code: "unionpay_register"}})
}

// Transition 校验所有收款状态变更，终态不允许再变更。
func Transition(from, to string) error {
	for _, allowed := range transitions[from] {
		if allowed == to {
			return nil
		}
	}
	return ops.Rule("收款状态不能转换：" + from + " → " + to)
}

var transitions = map[string][]string{
	"pending":    {"processing", "succeeded", "failed", "cancelled"},
	"processing": {"succeeded", "failed", "cancelled"},
	"succeeded":  {"refunded", "cancelled"},
}
