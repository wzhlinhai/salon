package ops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// SetDraftCreator 接入草稿存储，避免操作层依赖业务包。
func (r *Registry) SetDraftCreator(create func(context.Context, Env, string, json.RawMessage, string, string) (any, error)) {
	r.draftCreator = create
}

// SetDraftEditor 接入草稿修改前检查，避免操作层依赖业务包。
func (r *Registry) SetDraftEditor(check func(context.Context, Env, uuid.UUID) error) {
	r.draftEditor = check
}

// CheckDraftEdit 在调用者租户的写事务里按修改草稿的锁、可见性和状态规则检查草稿；错误与修改接口一致。
func (r *Registry) CheckDraftEdit(ctx context.Context, caller Caller, now time.Time, id uuid.UUID) error {
	if r.draftEditor == nil {
		return ErrDraftRequired
	}
	var committed error
	err := db.InTenant(ctx, r.pool, caller.TenantID, func(q *dbgen.Queries) error {
		err := r.draftEditor(ctx, Env{Now: now, Caller: caller, Q: q}, id)
		var rule *RuleError
		if errors.As(err, &rule) && rule.commit {
			committed = err
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	return committed
}

// SetDraftPreview 为可起草操作接入只读预览，避免草稿层依赖业务包。
func (r *Registry) SetDraftPreview(operation string, preview func(context.Context, Env, json.RawMessage) (any, error)) {
	if r.draftPreviews == nil {
		r.draftPreviews = map[string]func(context.Context, Env, json.RawMessage) (any, error){}
	}
	r.draftPreviews[operation] = preview
}

// PreviewDraft 在现有只读事务或新的只读事务中生成草稿预览；没有预览时返回 nil。
func (r *Registry) PreviewDraft(ctx context.Context, env Env, operation string, params json.RawMessage) (json.RawMessage, error) {
	preview := r.draftPreviews[operation]
	if preview == nil {
		return nil, nil
	}
	e, ok := r.ops[operation]
	if !ok {
		return nil, ErrUnknownOperation
	}
	if err := checkOperationPermission(env.Caller, e); err != nil {
		return nil, Rule("已无该操作权限，无法预览")
	}
	run := func(env Env) (json.RawMessage, error) {
		env.Grant = env.Caller.Grant(e.permission)
		out, err := preview(ctx, env, params)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	if env.Q != nil {
		return run(env)
	}
	env.Now = r.now()
	var out json.RawMessage
	err := db.InTenantReadOnly(ctx, r.pool, env.Caller.TenantID, func(q *dbgen.Queries) error {
		env.Q = q
		var err error
		out, err = run(env)
		return err
	})
	return out, err
}

// DraftPermission 返回可起草目标操作的主权限。
func (r *Registry) DraftPermission(name string) (Permission, error) {
	e, ok := r.ops[name]
	if !ok || e.ai != AIDraft {
		return "", ErrUnknownOperation
	}
	return e.permission, nil
}

// DraftVisible 报告非发起人能否看到目标操作的草稿：需要该操作的全部权限，且主权限的数据范围不是本人。
func (r *Registry) DraftVisible(c Caller, name string) (bool, error) {
	e, ok := r.ops[name]
	if !ok || e.ai != AIDraft {
		return false, ErrUnknownOperation
	}
	if checkOperationPermission(c, e) != nil {
		return false, nil
	}
	return c.Grant(e.permission).Scope != ScopeSelf, nil
}

// CheckDraft 校验目标可起草、调用者权限及与工具相同的参数 schema。
func (r *Registry) CheckDraft(c Caller, name string, params json.RawMessage) error {
	e, ok := r.ops[name]
	if !ok || e.ai != AIDraft {
		return ErrUnknownOperation
	}
	if err := checkOperationPermission(c, e); err != nil {
		return err
	}
	return r.validateArgs(e, params)
}

// PrepareDraft 复检确认人权限及参数，返回在现有事务内执行原 Handle 的函数。
func (r *Registry) PrepareDraft(c Caller, name string, params json.RawMessage) (func(context.Context, Env) (any, error), error) {
	if err := r.CheckDraft(c, name, params); err != nil {
		return nil, err
	}
	e := r.ops[name]
	return func(ctx context.Context, env Env) (any, error) {
		env.Grant = c.Grant(e.permission)
		return e.call(ctx, env, params)
	}, nil
}

func checkPermission(c Caller, p Permission) error {
	if !c.Has(p) {
		return ErrForbidden
	}
	return nil
}

func checkOperationPermission(c Caller, e *entry) error {
	if err := checkPermission(c, e.permission); err != nil {
		return err
	}
	for _, p := range e.alsoRequires {
		if err := checkPermission(c, p); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) allowed(c Caller, e *entry) bool {
	return checkOperationPermission(c, e) == nil
}

// ValidateDraftParams 只校验可起草目标及参数，供已有可见性授权的草稿修改使用。
func (r *Registry) ValidateDraftParams(name string, params json.RawMessage) error {
	if _, err := r.DraftPermission(name); err != nil {
		return err
	}
	return r.validateArgs(r.ops[name], params)
}
