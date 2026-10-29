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

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// PathPrefix 是所有业务操作的 HTTP 路径前缀。
const PathPrefix = "/api/ops/"

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type entry struct {
	name       string
	summary    string
	kind       Kind
	permission Permission
	ai         AIMode
	// call 把 JSON 参数解码为 I 并执行操作，返回 *O。
	call func(ctx context.Context, env Env, args []byte) (any, error)
}

// Registry 保存所有已声明的操作。
type Registry struct {
	pool *pgxpool.Pool
	api  huma.API
	ops  map[string]*entry
}

// NewRegistry 返回一个空的 Registry。注册的操作会挂到 api 上。
func NewRegistry(pool *pgxpool.Pool, api huma.API) *Registry {
	return &Registry{pool: pool, api: api, ops: map[string]*entry{}}
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
		name:       op.Name,
		summary:    op.Summary,
		kind:       op.Kind,
		permission: op.Permission,
		ai:         op.AI,
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
	case ai != AIDirect && ai != AIDraft && ai != AINone:
		return errors.New("AI mode is required")
	case kind == Write && ai == AIDirect:
		return errors.New("write operations cannot be executed by AI directly; use AIDraft or AINone (ADR-0004)")
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

// invoke 是所有调用的共同路径：渠道检查、权限检查、在调用者租户的事务中执行。
func (r *Registry) invoke(ctx context.Context, caller Caller, e *entry, run func(context.Context, Env) (any, error)) (any, error) {
	if caller.Channel == ChannelAI && e.ai == AINone {
		return nil, ErrUnknownOperation
	}
	if !caller.Has(e.permission) {
		return nil, ErrForbidden
	}
	if caller.Channel == ChannelAI && e.ai == AIDraft {
		// 草稿（ADR-0008）实现前，AI 对写操作的调用一律拒绝。
		return nil, ErrDraftRequired
	}
	var out any
	var committedRule error
	err := db.InTenant(ctx, r.pool, caller.TenantID, func(q *dbgen.Queries) error {
		var err error
		out, err = run(ctx, Env{Caller: caller, Grant: caller.Grant(e.permission), Q: q})
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
		if e.permission != LoggedIn && !seen[e.permission] {
			seen[e.permission] = true
			out = append(out, e.permission)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
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
	}
	return "none"
}
