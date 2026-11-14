package eval

import (
	"fmt"
	"sort"
)

// MetricDelta 是一项准确率的变化。
type MetricDelta struct {
	Name     string  `json:"name"`
	Old      float64 `json:"old"`
	New      float64 `json:"new"`
	Delta    float64 `json:"delta"`
	OldCount int     `json:"old_count,omitempty"`
	NewCount int     `json:"new_count,omitempty"`
}

// Flip 是一条用例的通过状态变化。
type Flip struct {
	ID     string `json:"id"`
	Story  string `json:"story"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Comparison 是两份评测报告的差异。
type Comparison struct {
	OldCaseFileHash string                   `json:"old_case_file_hash"`
	NewCaseFileHash string                   `json:"new_case_file_hash"`
	Metrics         []MetricDelta            `json:"metrics"`
	ByStory         map[string][]MetricDelta `json:"by_story"`
	Flips           []Flip                   `json:"flips"`
	OnlyOld         []string                 `json:"only_old,omitempty"`
	OnlyNew         []string                 `json:"only_new,omitempty"`
}

// Compare 比较两份报告；用例文件不同时要求 allowDifferent 才继续。
func Compare(oldReport, newReport Report, allowDifferent bool) (Comparison, error) {
	if oldReport.Run.CaseFileHash != newReport.Run.CaseFileHash && !allowDifferent {
		return Comparison{}, fmt.Errorf("两份报告的用例文件哈希不同（%s vs %s），不能直接比较；确认用例一致后加 --allow-different-cases", oldReport.Run.CaseFileHash, newReport.Run.CaseFileHash)
	}
	out := Comparison{
		OldCaseFileHash: oldReport.Run.CaseFileHash,
		NewCaseFileHash: newReport.Run.CaseFileHash,
		ByStory:         map[string][]MetricDelta{},
	}
	out.Metrics = metricDeltas(oldReport.Aggregates.Overall, newReport.Aggregates.Overall)
	stories := map[string]bool{}
	for story := range oldReport.Aggregates.ByStory {
		stories[story] = true
	}
	for story := range newReport.Aggregates.ByStory {
		stories[story] = true
	}
	for story := range stories {
		out.ByStory[story] = metricDeltas(oldReport.Aggregates.ByStory[story], newReport.Aggregates.ByStory[story])
	}
	oldCases := map[string]CaseResult{}
	newCases := map[string]CaseResult{}
	for _, result := range oldReport.Cases {
		oldCases[result.ID] = result
	}
	for _, result := range newReport.Cases {
		newCases[result.ID] = result
	}
	for id, before := range oldCases {
		after, ok := newCases[id]
		if !ok {
			out.OnlyOld = append(out.OnlyOld, id)
			continue
		}
		if before.Overall != after.Overall {
			from, to := "未通过", "通过"
			if before.Overall {
				from, to = "通过", "未通过"
			}
			out.Flips = append(out.Flips, Flip{ID: id, Story: after.Story, Before: from, After: to})
		}
	}
	for id := range newCases {
		if _, ok := oldCases[id]; !ok {
			out.OnlyNew = append(out.OnlyNew, id)
		}
	}
	sort.Strings(out.OnlyOld)
	sort.Strings(out.OnlyNew)
	sort.Slice(out.Flips, func(i, j int) bool { return out.Flips[i].ID < out.Flips[j].ID })
	return out, nil
}

func metricDeltas(oldMetrics, newMetrics Metrics) []MetricDelta {
	return []MetricDelta{
		{Name: "工具选择准确率", Old: oldMetrics.ToolSelectionAccuracy, New: newMetrics.ToolSelectionAccuracy, Delta: round(newMetrics.ToolSelectionAccuracy - oldMetrics.ToolSelectionAccuracy), OldCount: oldMetrics.ToolSelectionPass, NewCount: newMetrics.ToolSelectionPass},
		{Name: "参数准确率（逐次调用）", Old: oldMetrics.ArgumentCallAccuracy, New: newMetrics.ArgumentCallAccuracy, Delta: round(newMetrics.ArgumentCallAccuracy - oldMetrics.ArgumentCallAccuracy), OldCount: oldMetrics.ArgumentCallPass, NewCount: newMetrics.ArgumentCallPass},
		{Name: "参数准确率（逐条用例）", Old: oldMetrics.ArgumentCaseAccuracy, New: newMetrics.ArgumentCaseAccuracy, Delta: round(newMetrics.ArgumentCaseAccuracy - oldMetrics.ArgumentCaseAccuracy), OldCount: oldMetrics.ArgumentCasePass, NewCount: newMetrics.ArgumentCasePass},
		{Name: "回答准确率", Old: oldMetrics.AnswerAccuracy, New: newMetrics.AnswerAccuracy, Delta: round(newMetrics.AnswerAccuracy - oldMetrics.AnswerAccuracy), OldCount: oldMetrics.AnswerPass, NewCount: newMetrics.AnswerPass},
		{Name: "工具成功准确率", Old: oldMetrics.ToolSuccessAccuracy, New: newMetrics.ToolSuccessAccuracy, Delta: round(newMetrics.ToolSuccessAccuracy - oldMetrics.ToolSuccessAccuracy), OldCount: oldMetrics.ToolSuccessPass, NewCount: newMetrics.ToolSuccessPass},
		{Name: "总体通过率", Old: oldMetrics.OverallAccuracy, New: newMetrics.OverallAccuracy, Delta: round(newMetrics.OverallAccuracy - oldMetrics.OverallAccuracy), OldCount: oldMetrics.OverallPass, NewCount: newMetrics.OverallPass},
	}
}
