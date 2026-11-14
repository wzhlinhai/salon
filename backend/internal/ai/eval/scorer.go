package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

// Observed 是一次对话观察到的回答、工具来源和草稿。
type Observed struct {
	Reply      string
	Sources    []ai.Source
	Drafts     []ai.DraftView
	DraftTools map[string]bool
}

// ScoreCase 按工具选择、参数、回答和工具成功四项判定一条用例。
func ScoreCase(c Case, observed Observed, modelErr error) CaseResult {
	result := CaseResult{
		ID: c.ID, Story: c.Story, Category: c.Category, Role: c.Role,
		KnownGap: c.KnownGap != nil, Expected: c.Expected, Forbidden: c.Forbidden,
		AllowedTools: c.AllowedTools, AnswerNotContains: c.AnswerNotContains,
		ToolSelection: true, Arguments: true, Answer: true, ToolsOK: true,
		Sources: observed.Sources, Reply: observed.Reply,
	}
	if len(c.Messages) > 0 {
		result.Question = c.Messages[0].Content
	}
	if c.KnownGap != nil {
		result.KnownGapReason = c.KnownGap.Reason
	}
	for _, draft := range observed.Drafts {
		result.Drafts = append(result.Drafts, DraftRef{ID: draft.ID, Operation: draft.Operation, Status: draft.Status})
	}
	if modelErr != nil {
		result.FailureReasons = append(result.FailureReasons, modelFailureReason(modelErr))
		result.Answer = false
	}

	matched := make([]bool, len(observed.Sources))
	cursor := 0
	for i, expected := range c.Expected {
		result.ArgumentCallTotal++
		found := -1
		for j := cursor; j < len(observed.Sources); j++ {
			if expectedMatchesTool(expected, observed.Sources[j].Tool) {
				found = j
				break
			}
		}
		if found < 0 {
			result.ToolSelection = false
			result.Arguments = false
			if before := firstMatchingBefore(observed.Sources, cursor, expected); before >= 0 {
				reason := fmt.Sprintf("工具顺序不符：%s 应在第 %d 个调用", callNames(expected), i+1)
				result.FailureReasons = append(result.FailureReasons, reason)
				result.Diffs = append(result.Diffs, reason)
			} else {
				reason := fmt.Sprintf("缺少工具调用：%s", callNames(expected))
				result.FailureReasons = append(result.FailureReasons, reason)
				result.Diffs = append(result.Diffs, reason)
			}
			continue
		}
		matched[found] = true
		cursor = found + 1
		source := observed.Sources[found]
		if !source.OK {
			result.ToolsOK = false
			reason := fmt.Sprintf("工具执行失败：%s（%s）", source.Tool, source.Error)
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
		}
		mismatch := matchSubset(expectedArgsFor(expected, source.Tool), source.Arguments, observed.Drafts, "args")
		if mismatch != nil {
			result.Arguments = false
			reason := fmt.Sprintf("参数不匹配：%s 的 %s 期望 %s，实际 %s", source.Tool, mismatch.path, describe(mismatch.want), describe(mismatch.got))
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
		} else {
			result.ArgumentCallPass++
		}
	}

	for i, source := range observed.Sources {
		if matched[i] {
			continue
		}
		if !source.OK {
			result.ToolSelection = false
			reason := fmt.Sprintf("多余的工具调用失败：%s（%s）", source.Tool, source.Error)
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
			continue
		}
		if isDraftCall(source.Tool, observed.DraftTools) {
			result.ToolSelection = false
			reason := fmt.Sprintf("多余的起草工具调用：%s", source.Tool)
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
		}
	}

	if c.Refusal || c.Category == "refusal" {
		allowed := map[string]bool{}
		for _, name := range c.AllowedTools {
			allowed[name] = true
		}
		if len(observed.Drafts) > 0 {
			result.ToolSelection = false
			reason := fmt.Sprintf("拒绝类用例不应生成草稿：%s", draftNames(observed.Drafts))
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
		}
		for _, source := range observed.Sources {
			if !allowed[source.Tool] {
				result.ToolSelection = false
				reason := fmt.Sprintf("拒绝类用例调用了未允许的工具：%s", source.Tool)
				result.FailureReasons = append(result.FailureReasons, reason)
				result.Diffs = append(result.Diffs, reason)
			}
		}
	}

	for _, forbidden := range c.Forbidden {
		for _, source := range observed.Sources {
			if source.Tool == forbidden {
				result.ToolSelection = false
				reason := fmt.Sprintf("出现禁止的工具：%s", forbidden)
				result.FailureReasons = append(result.FailureReasons, reason)
				result.Diffs = append(result.Diffs, reason)
				break
			}
		}
	}

	for _, keyword := range c.AnswerContains {
		if !answerContains(observed.Reply, keyword) {
			result.Answer = false
			reason := fmt.Sprintf("回答缺少关键词：%s", keyword)
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
		}
	}
	for _, keyword := range c.AnswerNotContains {
		if answerContains(observed.Reply, keyword) {
			result.Answer = false
			reason := fmt.Sprintf("回答出现禁止关键词：%s", keyword)
			result.FailureReasons = append(result.FailureReasons, reason)
			result.Diffs = append(result.Diffs, reason)
		}
	}
	result.Overall = result.ToolSelection && result.Arguments && result.Answer && result.ToolsOK && modelErr == nil
	switch {
	case result.KnownGap:
		result.Status = "known_gap"
	case result.Overall:
		result.Status = "pass"
	default:
		result.Status = "fail"
	}
	return result
}

func isDraftCall(tool string, draftTools map[string]bool) bool {
	return tool == "update_draft_params" || draftTools[tool]
}

func draftNames(drafts []ai.DraftView) string {
	names := make([]string, len(drafts))
	for i, draft := range drafts {
		names[i] = draft.Operation
	}
	return strings.Join(names, "、")
}

func modelFailureReason(err error) string {
	switch {
	case errors.Is(err, ai.ErrTimeout):
		return "模型超时"
	case errors.Is(err, ai.ErrUnavailable):
		return "模型不可用"
	case errors.Is(err, ai.ErrToolLimit):
		return "工具调用次数超过上限"
	case errors.Is(err, ai.ErrNotConfigured):
		return "模型未配置"
	default:
		return "模型错误：" + err.Error()
	}
}

func callNames(call ExpectedCall) string {
	if call.Tool != "" {
		return call.Tool
	}
	return strings.Join(call.AnyOf, " 或 ")
}

func expectedMatchesTool(call ExpectedCall, tool string) bool {
	if call.Tool != "" {
		return call.Tool == tool
	}
	for _, name := range call.AnyOf {
		if name == tool {
			return true
		}
	}
	return false
}

func expectedArgsFor(call ExpectedCall, tool string) map[string]any {
	if call.Tool != "" {
		return call.Args
	}
	for i, name := range call.AnyOf {
		if name != tool {
			continue
		}
		if i < len(call.AnyOfArgs) && call.AnyOfArgs[i] != nil {
			return call.AnyOfArgs[i]
		}
		return call.Args
	}
	return call.Args
}

func firstMatchingBefore(sources []ai.Source, cursor int, call ExpectedCall) int {
	for i := 0; i < cursor && i < len(sources); i++ {
		if expectedMatchesTool(call, sources[i].Tool) {
			return i
		}
	}
	return -1
}

type mismatch struct {
	path      string
	want, got any
}

func matchSubset(want, got any, drafts []ai.DraftView, path string) *mismatch {
	if want == nil {
		return nil
	}
	want = resolveDraft(want, drafts)
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return &mismatch{path, want, got}
		}
		keys := make([]string, 0, len(w))
		for key := range w {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, exists := g[key]
			if !exists {
				return &mismatch{path + "." + key, w[key], "<缺失>"}
			}
			if m := matchSubset(w[key], value, drafts, path+"."+key); m != nil {
				return m
			}
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return &mismatch{path, want, got}
		}
		for i := range w {
			if m := matchSubset(w[i], g[i], drafts, fmt.Sprintf("%s[%d]", path, i)); m != nil {
				return m
			}
		}
		return nil
	case string:
		g, ok := got.(string)
		if !ok || !sameString(w, g) {
			return &mismatch{path, want, got}
		}
		return nil
	case float64:
		if !sameNumber(w, got) {
			return &mismatch{path, want, got}
		}
		return nil
	case bool:
		g, ok := got.(bool)
		if !ok || w != g {
			return &mismatch{path, want, got}
		}
		return nil
	case nil:
		if got != nil {
			return &mismatch{path, want, got}
		}
		return nil
	default:
		if fmt.Sprint(want) != fmt.Sprint(got) {
			return &mismatch{path, want, got}
		}
		return nil
	}
}

func resolveDraft(value any, drafts []ai.DraftView) any {
	text, ok := value.(string)
	if !ok {
		switch list := value.(type) {
		case []any:
			out := make([]any, len(list))
			for i, item := range list {
				out[i] = resolveDraft(item, drafts)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(list))
			for key, item := range list {
				out[key] = resolveDraft(item, drafts)
			}
			return out
		default:
			return value
		}
	}
	index := 0
	if text == "$draft" {
		index = 0
	} else if tail, found := strings.CutPrefix(text, "$draft:"); found {
		parsed, err := strconv.Atoi(tail)
		if err != nil || parsed < 1 {
			return "<草稿引用格式错误>"
		}
		index = parsed - 1
	} else {
		return value
	}
	if index < 0 || index >= len(drafts) {
		return "<没有草稿>"
	}
	return drafts[index].ID.String()
}

func sameString(want, got string) bool {
	if want == got {
		return true
	}
	if w, err := time.Parse(time.RFC3339Nano, want); err == nil {
		if g, err := time.Parse(time.RFC3339Nano, got); err == nil {
			return w.Equal(g)
		}
	}
	if w, err := uuid.Parse(want); err == nil {
		if g, err := uuid.Parse(got); err == nil {
			return w == g
		}
	}
	var w, g money.Amount
	if w.Scan(want) == nil && g.Scan(got) == nil {
		return w.Cmp(g) == 0
	}
	return false
}

func sameNumber(want float64, got any) bool {
	switch g := got.(type) {
	case float64:
		return want == g
	case json.Number:
		value, err := g.Float64()
		return err == nil && want == value
	case string:
		var expected, parsed money.Amount
		return expected.Scan(strconv.FormatFloat(want, 'f', 2, 64)) == nil && parsed.Scan(g) == nil && expected.Cmp(parsed) == 0
	default:
		return false
	}
}

func answerContains(reply, keyword string) bool {
	if strings.Contains(reply, keyword) {
		return true
	}
	return strings.Contains(strings.ToLower(reply), strings.ToLower(keyword))
}

func describe(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	text := string(raw)
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

func round(value float64) float64 { return math.Round(value*10000) / 10000 }

// Aggregate 计算一组用例的准确率；knownGap 为 true 时只统计已知缺口用例。
func Aggregate(results []CaseResult, knownGap bool) Aggregates {
	var included []CaseResult
	for _, result := range results {
		if result.KnownGap == knownGap {
			included = append(included, result)
		}
	}
	out := Aggregates{Overall: metrics(included), ByStory: map[string]Metrics{}, ByCategory: map[string]Metrics{}}
	byStory := map[string][]CaseResult{}
	byCategory := map[string][]CaseResult{}
	for _, result := range included {
		byStory[result.Story] = append(byStory[result.Story], result)
		byCategory[result.Category] = append(byCategory[result.Category], result)
	}
	for story, group := range byStory {
		out.ByStory[story] = metrics(group)
	}
	for category, group := range byCategory {
		out.ByCategory[category] = metrics(group)
	}
	return out
}

func metrics(results []CaseResult) Metrics {
	out := Metrics{Cases: len(results)}
	for _, result := range results {
		if result.ToolSelection {
			out.ToolSelectionPass++
		}
		out.ArgumentCallPass += result.ArgumentCallPass
		out.ArgumentCallTotal += result.ArgumentCallTotal
		if result.Arguments {
			out.ArgumentCasePass++
		}
		if result.Answer {
			out.AnswerPass++
		}
		if result.ToolsOK {
			out.ToolSuccessPass++
		}
		if result.Overall {
			out.OverallPass++
		}
	}
	if out.Cases > 0 {
		out.ToolSelectionAccuracy = round(float64(out.ToolSelectionPass) / float64(out.Cases))
		out.ArgumentCaseAccuracy = round(float64(out.ArgumentCasePass) / float64(out.Cases))
		out.AnswerAccuracy = round(float64(out.AnswerPass) / float64(out.Cases))
		out.ToolSuccessAccuracy = round(float64(out.ToolSuccessPass) / float64(out.Cases))
		out.OverallAccuracy = round(float64(out.OverallPass) / float64(out.Cases))
	}
	if out.ArgumentCallTotal > 0 {
		out.ArgumentCallAccuracy = round(float64(out.ArgumentCallPass) / float64(out.ArgumentCallTotal))
	} else if out.Cases > 0 {
		out.ArgumentCallAccuracy = 1
	}
	return out
}
