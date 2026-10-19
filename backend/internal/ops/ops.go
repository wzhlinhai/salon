// Package ops 是业务操作接口层：所有业务操作的唯一入口（ADR-0001）。
//
// 每个操作用 Operation 声明一次：名称、参数类型、返回类型、所需权限、AI 能否调用。
// Register 从这份声明同时生成 HTTP 接口（含 OpenAPI 文档）和 AI 工具定义。
// HTTP 调用和 AI 调用都经过 Registry.invoke：先检查权限，再在租户事务中执行操作。
package ops

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// Permission 是执行某类操作所需的权限，由角色授予员工。
type Permission string

// Kind 区分读操作和写操作。
type Kind int

const (
	Read Kind = iota + 1
	Write
)

// AIMode 声明 AI 能否调用一个操作、以什么方式调用。
type AIMode int

const (
	// AIDirect：AI 可以直接调用。只允许读操作。
	AIDirect AIMode = iota + 1
	// AIDraft：AI 调用时只能产生草稿，由有权限的人确认后才执行（ADR-0004）。写操作的默认做法。
	AIDraft
	// AINone：不向 AI 开放，例如团购核销（ADR-0004）。
	AINone
)

// Channel 是调用来自哪里。
type Channel int

const (
	ChannelHTTP Channel = iota + 1
	ChannelAI
)

// Caller 是发起调用的人。AI 调用使用发起人本人的 Caller，权限与本人一致。
type Caller struct {
	TenantID    uuid.UUID
	StoreID     uuid.UUID
	StaffID     uuid.UUID
	Permissions []Permission
	Channel     Channel
}

// Has 报告调用者是否有权限 p。
func (c Caller) Has(p Permission) bool { return slices.Contains(c.Permissions, p) }

type callerKey struct{}

// WithCaller 返回带有调用者的 context。
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom 取出 context 中的调用者。
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

var (
	// ErrUnauthenticated：没有登录或登录信息无效。
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrForbidden：调用者没有该操作所需的权限。
	ErrForbidden = errors.New("permission denied")
	// ErrDraftRequired：该操作只能通过草稿执行，不能由 AI 直接执行。
	ErrDraftRequired = errors.New("operation requires a draft confirmed by a person")
	// ErrUnknownOperation：没有这个操作，或者它不向当前渠道开放。
	ErrUnknownOperation = errors.New("unknown operation")
	// ErrNotFound：操作的目标不存在（或不属于当前租户）。
	ErrNotFound = errors.New("not found")
)

// Env 是操作执行时可用的环境。Q 已处于调用者租户的事务中。
type Env struct {
	Caller Caller
	Q      *dbgen.Queries
}

// Operation 声明一个业务操作。I 是参数，O 是返回值，二者都是普通的 JSON 结构体，
// 字段上的 doc、minLength 等标签同时用于 HTTP 校验和 AI 工具定义。
type Operation[I, O any] struct {
	// Name 是操作名，也是 HTTP 路径的最后一段和 AI 工具名。只用小写字母、数字和下划线。
	Name string
	// Summary 是面向人和 AI 的一句话说明。
	Summary    string
	Kind       Kind
	Permission Permission
	AI         AIMode
	Handle     func(ctx context.Context, env Env, in *I) (*O, error)
}
