package eval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// NewOracle 返回一条用例的 oracle 模型：按期望调用顺序发出工具调用，最后给出标准答案。
func NewOracle(c Case, f *Fixture) ai.Model {
	return &oracleModel{caseID: c.ID, calls: c.Expected, answer: c.ReferenceAnswer, fixture: f}
}

type oracleModel struct {
	caseID  string
	calls   []ExpectedCall
	answer  string
	fixture *Fixture
	call    int
}

func (m *oracleModel) Chat(_ context.Context, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
	if m.call >= len(m.calls) {
		return ai.Message{Role: ai.RoleAssistant, Content: m.answer}, nil
	}
	expected := m.calls[m.call]
	tool := expected.Tool
	if tool == "" && len(expected.AnyOf) > 0 {
		tool = expected.AnyOf[0]
	}
	args, err := oracleArgs(tool, expectedArgsFor(expected, tool), messages, m.fixture)
	if err != nil {
		return ai.Message{}, fmt.Errorf("oracle %s: %w", m.caseID, err)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ai.Message{}, err
	}
	m.call++
	return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{
		ID:        fmt.Sprintf("oracle_%d", m.call),
		Name:      tool,
		Arguments: string(raw),
	}}}, nil
}

func oracleArgs(tool string, expected map[string]any, messages []ai.Message, f *Fixture) (map[string]any, error) {
	args, _ := deepCopy(expected).(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	if tool == "update_draft_params" {
		draft := lastDraft(messages)
		if draft == nil {
			return nil, fmt.Errorf("update_draft_params 前没有草稿返回")
		}
		params, _ := draft["params"].(map[string]any)
		params, _ = deepCopy(params).(map[string]any)
		if params == nil {
			params = map[string]any{}
		}
		if overlay, ok := args["params"].(map[string]any); ok {
			mergeMap(params, overlay)
		}
		args["params"] = params
		args["id"] = draft["id"]
	}
	ownerID := f.Callers["owner"].StaffID.String()
	switch tool {
	case "open_order", "open_groupbuy_order":
		if _, ok := args["designated"]; !ok {
			args["designated"] = false
		}
	case "open_card", "recharge_card":
		if _, ok := args["sales_staff_ids"]; !ok {
			args["sales_staff_ids"] = []any{ownerID}
		}
		if _, ok := args["bonus"]; !ok {
			args["bonus"] = "0.00"
		}
		if _, ok := args["payments"]; !ok {
			args["payments"] = cashPayments(args["principal"])
		}
	case "adjust_card", "reprice_order_item", "void_order":
		if _, ok := args["reason"]; !ok {
			args["reason"] = "评测用例"
		}
	}
	return args, nil
}

func cashPayments(principal any) []any {
	amount, ok := principal.(string)
	if !ok || amount == "" || amount == "0" || amount == "0.00" {
		return []any{}
	}
	return []any{map[string]any{"method": "cash", "amount": amount}}
}

func lastDraft(messages []ai.Message) map[string]any {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != ai.RoleTool {
			continue
		}
		var draft map[string]any
		if json.Unmarshal([]byte(messages[i].Content), &draft) != nil {
			continue
		}
		if draft["id"] != nil && draft["params"] != nil {
			return draft
		}
	}
	return nil
}

func deepCopy(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = deepCopy(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = deepCopy(item)
		}
		return out
	default:
		return value
	}
}

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			if base, ok := dst[key].(map[string]any); ok {
				mergeMap(base, child)
				continue
			}
		}
		dst[key] = deepCopy(value)
	}
}
