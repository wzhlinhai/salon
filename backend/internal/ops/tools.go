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
	// Draft 为 true 时是写操作，AI 只能起草，由人确认后执行（ADR-0004）。调用只创建草稿。
	Draft bool
}

// ReadToolListDrafts、ReadToolGetDraft 和 ReadToolGetRedemption 虽然声明为 Read + AIDirect，
// 执行时仍会写入：前两个读取消时会惰性过期草稿，get_redemption 会写审计。对话暂不提供，
// 等这些读操作拆出惰性写入后（另开 issue）再从排除集合移除。
const (
	ReadToolListDrafts    = "list_drafts"
	ReadToolGetDraft      = "get_draft"
	ReadToolGetRedemption = "get_redemption"
)

var excludedReadTools = map[string]struct{}{
	ReadToolListDrafts:    {},
	ReadToolGetDraft:      {},
	ReadToolGetRedemption: {},
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
			// 工具调用只创建草稿。
			desc += "（只创建草稿，须由人确认后执行）"
		}
		tools = append(tools, Tool{
			Name:        e.name,
			Description: desc,
			Parameters:  r.toolSchema(e),
			Draft:       e.ai == AIDraft,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// ReadTools 返回 caller 可以让 AI 直接读取的工具：只包括 Read + AIDirect 的操作。
// 写操作、草稿操作和 AINone 操作都不会出现在对话可用的工具列表里。
func (r *Registry) ReadTools(caller Caller) []Tool {
	tools := r.Tools(caller)
	out := tools[:0]
	for _, tool := range tools {
		e, ok := r.ops[tool.Name]
		if !ok || e.kind != Read || e.ai != AIDirect || readToolExcluded(tool.Name) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// CallReadTool 执行 AI 对话中的一次只读工具调用。不是 Read + AIDirect 的操作，或带有惰性写入而被排除的操作，一律当作未知工具。
func (r *Registry) CallReadTool(ctx context.Context, caller Caller, name string, args json.RawMessage) (json.RawMessage, error) {
	e, ok := r.ops[name]
	if !ok || e.kind != Read || e.ai != AIDirect || readToolExcluded(name) {
		return nil, ErrUnknownOperation
	}
	return r.CallTool(ctx, caller, name, args)
}

func readToolExcluded(name string) bool {
	_, ok := excludedReadTools[name]
	return ok
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
	utterance, summary := "", e.summary
	if e.ai == AIDraft {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(args, &fields); err != nil {
			return nil, &ToolError{Messages: []string{"arguments must be an object"}}
		}
		for key, dst := range map[string]*string{"utterance": &utterance, "summary": &summary} {
			if raw, ok := fields[key]; ok {
				if string(raw) == "null" {
					return nil, &ToolError{Messages: []string{key + " must be a string"}}
				}
				if err := json.Unmarshal(raw, dst); err != nil {
					return nil, &ToolError{Messages: []string{key + " must be a string"}}
				}
				delete(fields, key)
			}
		}
		args, _ = json.Marshal(fields)
	}
	if err := r.validateArgs(e, args); err != nil {
		return nil, err
	}
	out, err := r.invoke(ctx, caller, e, func(ctx context.Context, env Env) (any, error) {
		if e.ai == AIDraft {
			if r.draftCreator == nil {
				return nil, ErrDraftRequired
			}
			return r.draftCreator(ctx, env, name, args, utterance, summary)
		}
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

func (r *Registry) toolSchema(e *entry) map[string]any {
	schema := r.parametersSchema(e)
	if e.ai == AIDraft {
		props := schema["properties"].(map[string]any)
		props["summary"] = map[string]any{"type": "string", "description": "面向人的草稿摘要"}
		props["utterance"] = map[string]any{"type": "string", "description": "员工原始口述"}
	}
	return schema
}
