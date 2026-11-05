package ops

import (
	"context"
	"encoding/json"
)

// SetDraftCreator 接入草稿存储，避免操作层依赖业务包。
func (r *Registry) SetDraftCreator(create func(context.Context, Env, string, json.RawMessage, string, string) (any, error)) {
	r.draftCreator = create
}

// DraftPermission 返回可起草目标操作的权限。
func (r *Registry) DraftPermission(name string) (Permission, error) {
	e, ok := r.ops[name]
	if !ok || e.ai != AIDraft {
		return "", ErrUnknownOperation
	}
	return e.permission, nil
}

// CheckDraft 校验目标可起草、调用者权限及与工具相同的参数 schema。
func (r *Registry) CheckDraft(c Caller, name string, params json.RawMessage) error {
	p, err := r.DraftPermission(name)
	if err != nil {
		return err
	}
	if err := checkPermission(c, p); err != nil {
		return err
	}
	return r.validateArgs(r.ops[name], params)
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

// ValidateDraftParams 只校验可起草目标及参数，供已有可见性授权的草稿修改使用。
func (r *Registry) ValidateDraftParams(name string, params json.RawMessage) error {
	if _, err := r.DraftPermission(name); err != nil {
		return err
	}
	return r.validateArgs(r.ops[name], params)
}
