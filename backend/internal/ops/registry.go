package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// PathPrefix 是所有业务操作的 HTTP 路径前缀。
const PathPrefix = "/api/ops/"

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type entry struct {
	name         string
	summary      string
	kind         Kind
	permission   Permission
	alsoRequires []Permission
	ai           AIMode
	phased       bool
	locking      bool
	// call 把 JSON 参数解码为 I 并执行操作，返回 *O。
	call func(ctx context.Context, env Env, args []byte) (any, error)
}

// Registry 保存所有已声明的操作。
type Registry struct {
	pool          *pgxpool.Pool
	now           func() time.Time
	api           huma.API
	ops           map[string]*entry
	draftCreator  func(context.Context, Env, string, json.RawMessage, string, string) (any, error)
	draftPreviews map[string]func(context.Context, Env, json.RawMessage) (any, error)
}

// NewRegistry 返回一个空的 Registry。注册的操作会挂到 api 上。
func NewRegistry(pool *pgxpool.Pool, api huma.API, now func() time.Time) *Registry {
	return &Registry{pool: pool, now: now, api: api, ops: map[string]*entry{}}
}

// Register 声明一个操作，并生成它的 HTTP 接口。声明不合规时 panic，在启动时暴露错误。
func Register[I, O any](r *Registry, op Operation[I, O]) {
	if err := validate(op.Name, op.Kind, op.Permission, op.AI, op.Handle != nil); err != nil {
		panic(fmt.Sprintf("ops: invalid operation %q: %v", op.Name, err))
	}
	if err := validateInputDefaults(reflect.TypeFor[I]()); err != nil {
		panic(fmt.Sprintf("ops: invalid operation %q: %v", op.Name, err))
	}
	if _, dup := r.ops[op.Name]; dup {
		panic(fmt.Sprintf("ops: duplicate operation %q", op.Name))
	}
	e := &entry{
		name:         op.Name,
		summary:      op.Summary,
		kind:         op.Kind,
		permission:   op.Permission,
		alsoRequires: append([]Permission{}, op.AlsoRequires...),
		ai:           op.AI,
		phased:       op.Phased,
		locking:      op.Locking,
		call: func(ctx context.Context, env Env, args []byte) (any, error) {
			in := new(I)
			if err := json.Unmarshal(args, in); err != nil {
				return nil, err
			}
			return op.Handle(ctx, env, in)
		},
	}
	r.ops[op.Name] = e

	huma.Register(r.api, huma.Operation{
		OperationID: op.Name,
		Method:      http.MethodPost,
		Path:        PathPrefix + op.Name,
		Summary:     op.Summary,
		Extensions: map[string]any{
			"x-permission": string(op.Permission),
			"x-ai":         aiModeName(op.AI),
		},
	}, func(ctx context.Context, req *struct{ Body I }) (*struct{ Body *O }, error) {
		caller, ok := CallerFrom(ctx)
		if !ok {
			return nil, httpError(ErrUnauthenticated)
		}
		caller.Channel = ChannelHTTP
		out, err := r.invoke(ctx, caller, e, func(ctx context.Context, env Env) (any, error) {
			return op.Handle(ctx, env, &req.Body)
		})
		if err != nil {
			return nil, httpError(err)
		}
		return &struct{ Body *O }{Body: out.(*O)}, nil
	})
}

func validate(name string, kind Kind, perm Permission, ai AIMode, hasHandler bool) error {
	switch {
	case !namePattern.MatchString(name):
		return errors.New("name must match " + namePattern.String())
	case kind != Read && kind != Write:
		return errors.New("kind must be Read or Write")
	case perm == "":
		return errors.New("permission is required")
	case ai != AIDirect && ai != AIDraft && ai != AINone && ai != AIManageDraft:
		return errors.New("AI mode is required")
	case kind == Write && ai == AIDirect:
		return errors.New("write operations cannot be executed by AI directly; use AIDraft or AINone (ADR-0004)")
	case ai == AIManageDraft && (kind != Write || (name != "create_draft" && name != "update_draft_params" && name != "reject_draft")):
		return errors.New("draft control mode is reserved for draft metadata operations")
	case !hasHandler:
		return errors.New("handler is required")
	}
	return nil
}

// validateInputDefaults 拒绝只在 HTTP 路径生效的 huma 默认值。
func validateInputDefaults(input reflect.Type) error {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string) error
	walk = func(t reflect.Type, path string) error {
		if seen[t] {
			return nil
		}
		seen[t] = true
		switch t.Kind() {
		case reflect.Pointer:
			return walk(t.Elem(), path)
		case reflect.Slice, reflect.Array:
			return walk(t.Elem(), path+"[]")
		case reflect.Map:
			return walk(t.Elem(), path+"[value]")
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				fieldPath := field.Name
				if path != "" {
					fieldPath = path + "." + field.Name
				}
				if _, ok := field.Tag.Lookup("default"); ok {
					return fmt.Errorf("field %s: default must be set in the handler because the AI path does not apply huma defaults", fieldPath)
				}
				if err := walk(field.Type, fieldPath); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(input, "")
}

// invoke 是所有调用的共同路径：渠道、权限检查后按操作声明提供租户事务。
func (r *Registry) invoke(ctx context.Context, caller Caller, e *entry, run func(context.Context, Env) (any, error)) (any, error) {
	if caller.Channel == ChannelAI && e.ai == AINone {
		return nil, ErrUnknownOperation
	}
	if err := checkOperationPermission(caller, e); err != nil {
		return nil, err
	}
	now := r.now()
	if e.phased {
		transact := func(ctx context.Context, fn func(*dbgen.Queries) error) error {
			return db.InTenant(ctx, r.pool, caller.TenantID, fn)
		}
		if e.kind == Read && e.ai == AIDirect && !e.locking {
			transact = func(ctx context.Context, fn func(*dbgen.Queries) error) error {
				return db.InTenantReadOnly(ctx, r.pool, caller.TenantID, fn)
			}
		}
		return run(ctx, Env{Now: now, Caller: caller, Grant: caller.Grant(e.permission), Transact: transact})
	}
	var out any
	var committedRule error
	inTenant := db.InTenant
	if e.kind == Read && !e.locking {
		inTenant = db.InTenantReadOnly
	}
	err := inTenant(ctx, r.pool, caller.TenantID, func(q *dbgen.Queries) error {
		var err error
		out, err = run(ctx, Env{Now: now, Caller: caller, Grant: caller.Grant(e.permission), Q: q})
		var rule *RuleError
		if errors.As(err, &rule) && rule.commit {
			committedRule = err
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, committedRule
}

// Permissions 返回所有已声明操作用到的权限，按名称排序。供角色设置使用。
func (r *Registry) Permissions() []Permission {
	seen := map[Permission]bool{}
	var out []Permission
	for _, e := range r.ops {
		for _, p := range append([]Permission{e.permission}, e.alsoRequires...) {
			if p != LoggedIn && p != CustomerSelf && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// OperationInfo 描述一个已注册操作的权限和调用方式，供权限审查使用。
type OperationInfo struct {
	Name       string
	Kind       Kind
	Permission Permission
	AI         AIMode
}

// Operations 返回全部已注册操作，按名称排序。
func (r *Registry) Operations() []OperationInfo {
	out := make([]OperationInfo, 0, len(r.ops))
	for _, e := range r.ops {
		out = append(out, OperationInfo{Name: e.name, Kind: e.kind, Permission: e.permission, AI: e.ai})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Authorize 只检查调用者是否有权调用某个已注册操作，不执行操作；供权限审查使用。
func (r *Registry) Authorize(caller Caller, name string) error {
	e, ok := r.ops[name]
	if !ok {
		return ErrUnknownOperation
	}
	return checkOperationPermission(caller, e)
}

// StatusOf 把操作返回的错误对应到 HTTP 状态码。HTTP 接口按它返回；AI 路径的测试也按它比较两条路径的结果。
func StatusOf(err error) int {
	var toolErr *ToolError
	var rule *RuleError
	switch {
	case errors.As(err, &toolErr):
		return http.StatusUnprocessableEntity
	case errors.As(err, &rule), errors.Is(err, ErrDraftRequired):
		return http.StatusConflict
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrUnknownOperation):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func httpError(err error) error {
	var v huma.StatusError
	if errors.As(err, &v) {
		return v
	}
	status := StatusOf(err)
	if status == http.StatusInternalServerError {
		slog.Error("operation failed", "err", err)
		return huma.Error500InternalServerError("internal error")
	}
	return huma.NewError(status, err.Error())
}

func aiModeName(m AIMode) string {
	switch m {
	case AIDirect:
		return "direct"
	case AIDraft:
		return "draft"
	case AIManageDraft:
		return "draft_control"
	}
	return "none"
}
