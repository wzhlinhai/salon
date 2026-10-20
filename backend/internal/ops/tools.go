package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Tool 是一个 AI 工具定义，格式对应 OpenAI 兼容 Chat Completions 的 function tool。
type Tool struct {
	Name        string
	Description string
	// Parameters 是参数的 JSON Schema，取自同一操作 HTTP 请求体的 OpenAPI schema，引用已展开。
	Parameters map[string]any
	// Draft 为 true 时是写操作，AI 只能起草，由人确认后执行（ADR-0004）。草稿实现前调用会被拒绝。
	Draft bool
}

// ToolError 是 AI 工具调用的参数错误，信息可以原样返回给模型，让它修正参数。
type ToolError struct{ Messages []string }

func (e *ToolError) Error() string { return "invalid arguments: " + strings.Join(e.Messages, "; ") }

// Tools 返回 caller 可以让 AI 调用的工具：操作向 AI 开放，且 caller 本人有权限。
func (r *Registry) Tools(caller Caller) []Tool {
	var tools []Tool
	for _, e := range r.ops {
		if e.ai == AINone || !caller.Has(e.permission) {
			continue
		}
		desc := e.summary
		if e.ai == AIDraft {
			// 草稿（P0-29）实现前，调用会被拒绝。如实告诉模型，让它请员工到页面上操作。
			desc += "（这是写操作，目前还不能由 AI 执行：调用会被拒绝，请让员工在页面上完成）"
		}
		tools = append(tools, Tool{
			Name:        e.name,
			Description: desc,
			Parameters:  r.parametersSchema(e),
			Draft:       e.ai == AIDraft,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// CallTool 以 caller 本人的权限执行 AI 的一次工具调用。args 是模型给出的 JSON 参数。
// 参数用 HTTP 接口同一份 schema 校验。
func (r *Registry) CallTool(ctx context.Context, caller Caller, name string, args json.RawMessage) (json.RawMessage, error) {
	caller.Channel = ChannelAI
	e, ok := r.ops[name]
	if !ok || e.ai == AINone {
		return nil, ErrUnknownOperation
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := r.validateArgs(e, args); err != nil {
		return nil, err
	}
	out, err := r.invoke(ctx, caller, e, func(ctx context.Context, env Env) (any, error) {
		return e.call(ctx, env, args)
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (r *Registry) requestSchema(e *entry) *huma.Schema {
	item := r.api.OpenAPI().Paths[PathPrefix+e.name]
	return item.Post.RequestBody.Content["application/json"].Schema
}

func (r *Registry) validateArgs(e *entry, args json.RawMessage) error {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return &ToolError{Messages: []string{"arguments are not valid JSON: " + err.Error()}}
	}
	res := &huma.ValidateResult{}
	huma.Validate(r.api.OpenAPI().Components.Schemas, r.requestSchema(e), huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
	if len(res.Errors) == 0 {
		return nil
	}
	msgs := make([]string, len(res.Errors))
	for i, err := range res.Errors {
		msgs[i] = err.Error()
	}
	return &ToolError{Messages: msgs}
}

// parametersSchema 把 HTTP 请求体的 schema 转成 JSON，并展开其中的 $ref。
func (r *Registry) parametersSchema(e *entry) map[string]any {
	raw, err := json.Marshal(r.requestSchema(e))
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	params := r.deref(m, map[string]bool{}).(map[string]any)
	// 没有参数的操作，Huma 生成的 schema 没有 properties；部分 OpenAI 兼容接口要求有这个字段。
	if _, ok := params["properties"]; !ok {
		params["properties"] = map[string]any{}
	}
	return params
}

func (r *Registry) deref(v any, visiting map[string]bool) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			if visiting[ref] {
				panic(fmt.Sprintf("ops: recursive schema %s cannot be used as AI tool parameters", ref))
			}
			s := r.api.OpenAPI().Components.Schemas.SchemaFromRef(ref)
			raw, _ := json.Marshal(s)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			visiting[ref] = true
			out := r.deref(m, visiting)
			delete(visiting, ref)
			return out
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = r.deref(val, visiting)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.deref(val, visiting)
		}
		return out
	}
	return v
}
