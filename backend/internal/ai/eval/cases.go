package eval

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed cases.json
var caseFiles embed.FS

var roles = map[string]bool{"owner": true, "manager": true, "stylist": true, "assistant": true}

// LoadSuite 读取嵌入的用例文件并计算内容哈希。
func LoadSuite() (*Suite, error) {
	raw, err := caseFiles.ReadFile("cases.json")
	if err != nil {
		return nil, err
	}
	cases, err := ParseCases(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &Suite{Cases: cases, Hash: hex.EncodeToString(sum[:]), Raw: raw}, nil
}

// ParseCases 解析并校验用例 JSON。
func ParseCases(raw []byte) ([]Case, error) {
	var cases []Case
	if err := json.Unmarshal(raw, &cases); err != nil {
		return nil, fmt.Errorf("parse cases.json: %w", err)
	}
	if len(cases) < 30 || len(cases) > 50 {
		return nil, fmt.Errorf("评测用例须为 30–50 条，当前 %d 条", len(cases))
	}
	seen := map[string]bool{}
	for i, c := range cases {
		if c.ID == "" || seen[c.ID] {
			return nil, fmt.Errorf("第 %d 条用例 id 为空或重复：%q", i+1, c.ID)
		}
		seen[c.ID] = true
		if !roles[c.Role] {
			return nil, fmt.Errorf("%s: 未知角色 %q", c.ID, c.Role)
		}
		if c.Story == "" || c.Category == "" {
			return nil, fmt.Errorf("%s: story 和 category 不能为空", c.ID)
		}
		if len(c.Messages) == 0 || c.Messages[0].Role != "user" {
			return nil, fmt.Errorf("%s: 至少一条 user 消息", c.ID)
		}
		for _, message := range c.Messages {
			if message.Role != "user" && message.Role != "assistant" {
				return nil, fmt.Errorf("%s: 消息角色只能是 user 或 assistant", c.ID)
			}
			if strings.TrimSpace(message.Content) == "" {
				return nil, fmt.Errorf("%s: 消息内容不能为空", c.ID)
			}
		}
		for _, call := range c.Expected {
			if (call.Tool == "") == (len(call.AnyOf) == 0) {
				return nil, fmt.Errorf("%s: 每条期望调用必须且只能填 tool 或 any_of", c.ID)
			}
			if len(call.AnyOfArgs) > 0 && (call.Tool != "" || len(call.AnyOfArgs) != len(call.AnyOf)) {
				return nil, fmt.Errorf("%s: any_of_args 只能与 any_of 成对使用且长度相同", c.ID)
			}
		}
		if len(c.AnswerContains) == 0 {
			return nil, fmt.Errorf("%s: answer_contains 不能为空", c.ID)
		}
		if strings.TrimSpace(c.ReferenceAnswer) == "" {
			return nil, fmt.Errorf("%s: reference_answer 不能为空", c.ID)
		}
		if c.KnownGap != nil && strings.TrimSpace(c.KnownGap.Reason) == "" {
			return nil, fmt.Errorf("%s: known_gap.reason 不能为空", c.ID)
		}
	}
	return cases, nil
}

// ResolveCase 把用例里的符号引用和日期宏替换成固定门店里的实际值，不修改传入的用例。
func ResolveCase(c Case, f *Fixture) (Case, error) {
	out := c
	out.Messages = append([]CaseMessage(nil), c.Messages...)
	out.Forbidden = append([]string(nil), c.Forbidden...)
	out.AllowedTools = append([]string(nil), c.AllowedTools...)
	out.AnswerContains = make([]string, len(c.AnswerContains))
	out.AnswerNotContains = make([]string, len(c.AnswerNotContains))
	out.Expected = make([]ExpectedCall, len(c.Expected))
	for i := range c.Expected {
		out.Expected[i] = c.Expected[i]
		out.Expected[i].AnyOf = append([]string(nil), c.Expected[i].AnyOf...)
		if c.Expected[i].Args != nil {
			resolved, err := resolveValue(c.Expected[i].Args, f)
			if err != nil {
				return Case{}, fmt.Errorf("%s 第 %d 条期望调用：%w", c.ID, i+1, err)
			}
			out.Expected[i].Args = resolved.(map[string]any)
		}
		out.Expected[i].AnyOfArgs = make([]map[string]any, len(c.Expected[i].AnyOfArgs))
		for j, args := range c.Expected[i].AnyOfArgs {
			if args == nil {
				continue
			}
			resolved, err := resolveValue(args, f)
			if err != nil {
				return Case{}, fmt.Errorf("%s 第 %d 条期望调用第 %d 个备选：%w", c.ID, i+1, j+1, err)
			}
			out.Expected[i].AnyOfArgs[j] = resolved.(map[string]any)
		}
	}
	for i, keyword := range c.AnswerContains {
		resolved, err := f.ResolveString(keyword)
		if err != nil {
			return Case{}, fmt.Errorf("%s answer_contains：%w", c.ID, err)
		}
		out.AnswerContains[i] = resolved
	}
	for i, keyword := range c.AnswerNotContains {
		resolved, err := f.ResolveString(keyword)
		if err != nil {
			return Case{}, fmt.Errorf("%s answer_not_contains：%w", c.ID, err)
		}
		out.AnswerNotContains[i] = resolved
	}
	return out, nil
}

func resolveValue(v any, f *Fixture) (any, error) {
	switch t := v.(type) {
	case string:
		return f.ResolveString(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			resolved, err := resolveValue(item, f)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for key, item := range t {
			resolved, err := resolveValue(item, f)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	default:
		return v, nil
	}
}
