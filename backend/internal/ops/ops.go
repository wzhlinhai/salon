// Package ops 是业务操作接口层：所有业务操作的唯一入口（ADR-0001）。
//
// 每个操作用 Operation 声明一次：名称、参数类型、返回类型、所需权限、AI 能否调用。
// Register 从这份声明同时生成 HTTP 接口（含 OpenAPI 文档）和 AI 工具定义。
// HTTP 调用和 AI 调用都经过 Registry.invoke：先检查权限，再在租户事务中执行操作。
package ops

import (
	"context"
	"errors"

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

// LoggedIn 表示只要是已登录的在职员工就可以调用，例如查看自己的信息。
const LoggedIn Permission = "logged_in"

// Scope 是权限的数据范围（ADR-0009）。
type Scope string

const (
	// ScopeSelf：只限本人作为负责人或参与员工出现过的单、本人的业绩、本人负责的顾客。
	ScopeSelf Scope = "self"
	// ScopeStore：本门店全部。
	ScopeStore Scope = "store"
)

// Grant 是调用者拥有的一项权限及其限制。
type Grant struct {
	// Scope 为空表示该权限不区分数据范围。
	Scope Scope
	// WindowDays 为 nil 表示不限时间；0 表示只限当天（营业日）；N 表示 N 天内。
	WindowDays *int
}

// Covers 报告 g 是否不窄于 other：数据范围和时间窗口都至少一样宽。
func (g Grant) Covers(other Grant) bool {
	if g.Scope == ScopeSelf && other.Scope == ScopeStore {
		return false
	}
	if g.WindowDays != nil && (other.WindowDays == nil || *other.WindowDays > *g.WindowDays) {
		return false
	}
	return true
}

// Widen 返回 g 与 other 中较宽的限制组合，用于多个角色取并集。
func (g Grant) Widen(other Grant) Grant {
	var out Grant
	switch {
	case g.Scope == ScopeStore || other.Scope == ScopeStore:
		out.Scope = ScopeStore
	case g.Scope == ScopeSelf || other.Scope == ScopeSelf:
		out.Scope = ScopeSelf
	}
	if g.WindowDays != nil && other.WindowDays != nil {
		out.WindowDays = new(max(*g.WindowDays, *other.WindowDays))
	}
	return out
}

// Caller 是发起调用的人。AI 调用使用发起人本人的 Caller，权限与本人一致（ADR-0009）。
type Caller struct {
	TenantID uuid.UUID
	StoreID  uuid.UUID
	StaffID  uuid.UUID
	// Owner：调用者有老板角色。只有老板能授予或去掉老板角色（ADR-0009）。
	Owner       bool
	Permissions map[Permission]Grant
	Channel     Channel
}

// Has 报告调用者是否有权限 p。
func (c Caller) Has(p Permission) bool {
	if p == LoggedIn {
		return c.StaffID != uuid.Nil
	}
	_, ok := c.Permissions[p]
	return ok
}

// Grant 返回调用者在权限 p 上的限制。
func (c Caller) Grant(p Permission) Grant { return c.Permissions[p] }

// ScopeFilter 返回调用者在权限 p 上的数据范围过滤：没有该权限时 ok 为 false；范围是本人时 self 为调用者的员工 ID，否则为 nil。
func (c Caller) ScopeFilter(p Permission) (ok bool, self *uuid.UUID) {
	if !c.Has(p) {
		return false, nil
	}
	if c.Grant(p).Scope == ScopeSelf {
		return true, &c.StaffID
	}
	return true, nil
}

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

// RuleError 表示请求违反了业务规则，例如删掉最后一名在职老板。Message 面向人和 AI。
type RuleError struct{ Message string }

func (e *RuleError) Error() string { return e.Message }

// Rule 返回一个 RuleError。
func Rule(msg string) error { return &RuleError{Message: msg} }

// Env 是操作执行时可用的环境。Q 已处于调用者租户的事务中。
type Env struct {
	Caller Caller
	// Grant 是调用者在该操作所需权限上的限制。有数据范围或时间窗口的操作要按它过滤。
	Grant Grant
	Q     *dbgen.Queries
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
