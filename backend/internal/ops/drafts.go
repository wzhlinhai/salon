package ops

import (
	"context"
	"encoding/json"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// SetDraftCreator 接入草稿存储，避免操作层依赖业务包。
func (r *Registry) SetDraftCreator(create func(context.Context, Env, string, json.RawMessage, string, string) (any, error)) {
	r.draftCreator = create
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
	run := func(env Env) (json.RawMessage, error) {
		if e, ok := r.ops[operation]; ok {
			env.Grant = env.Caller.Grant(e.permission)
		}
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

// DraftAllowed 报告调用者是否拥有起草或确认目标操作所需的全部权限。
func (r *Registry) DraftAllowed(c Caller, name string) (bool, error) {
	e, ok := r.ops[name]
	if !ok || e.ai != AIDraft {
		return false, ErrUnknownOperation
	}
	return checkOperationPermission(c, e) == nil, nil
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
