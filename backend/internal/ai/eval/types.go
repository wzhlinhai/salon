// Package eval 是 AI 对话的离线评测：用固定门店数据跑同一套店务问题，比较工具选择、参数和回答。
package eval

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
)

// KnownGap 是产品暂时无法满足、但仍保留在评测集中的用例原因。
type KnownGap struct {
	Reason string `json:"reason"`
}

// CaseMessage 是一条用户或助手消息。
type CaseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ExpectedCall 是一条期望的工具调用；Tool 与 AnyOf 二选一，Args 是参数子集。
// AnyOfArgs 与 AnyOf 平行，可为每个备选工具给出不同的参数子集。
type ExpectedCall struct {
	Tool      string           `json:"tool,omitempty"`
	AnyOf     []string         `json:"any_of,omitempty"`
	Args      map[string]any   `json:"args,omitempty"`
	AnyOfArgs []map[string]any `json:"any_of_args,omitempty"`
}

// Case 是一条真实店务问题和它的标准答案、期望工具调用。
type Case struct {
	ID                string         `json:"id"`
	Story             string         `json:"story"`
	Category          string         `json:"category"`
	Role              string         `json:"role"`
	Messages          []CaseMessage  `json:"messages"`
	Expected          []ExpectedCall `json:"expected_calls"`
	Forbidden         []string       `json:"forbidden_tools,omitempty"`
	AllowedTools      []string       `json:"allowed_tools,omitempty"`
	Refusal           bool           `json:"refusal,omitempty"`
	AnswerContains    []string       `json:"answer_contains"`
	AnswerNotContains []string       `json:"answer_not_contains,omitempty"`
	ReferenceAnswer   string         `json:"reference_answer"`
	KnownGap          *KnownGap      `json:"known_gap,omitempty"`
}

// Suite 是嵌入的用例文件和它的内容哈希。
type Suite struct {
	Cases []Case
	Hash  string
	Raw   []byte
}

// DraftRef 是报告里一张草稿的最小信息。
type DraftRef struct {
	ID        uuid.UUID `json:"id"`
	Operation string    `json:"operation"`
	Status    string    `json:"status"`
}

// CaseResult 是一条用例的记录：判定、来源、草稿、失败原因和差异。
type CaseResult struct {
	ID                string         `json:"id"`
	Story             string         `json:"story"`
	Category          string         `json:"category"`
	Question          string         `json:"question"`
	Role              string         `json:"role"`
	KnownGap          bool           `json:"known_gap"`
	KnownGapReason    string         `json:"known_gap_reason,omitempty"`
	ToolSelection     bool           `json:"tool_selection"`
	Arguments         bool           `json:"arguments"`
	ArgumentCallPass  int            `json:"argument_call_pass"`
	ArgumentCallTotal int            `json:"argument_call_total"`
	Answer            bool           `json:"answer"`
	ToolsOK           bool           `json:"tools_ok"`
	Overall           bool           `json:"overall"`
	Status            string         `json:"status"`
	LatencyMS         int64          `json:"latency_ms"`
	ModelCalls        int            `json:"model_calls"`
	PromptHash        string         `json:"prompt_hash,omitempty"`
	Expected          []ExpectedCall `json:"expected"`
	Forbidden         []string       `json:"forbidden_tools,omitempty"`
	AllowedTools      []string       `json:"allowed_tools,omitempty"`
	OfferedTools      []string       `json:"offered_tools,omitempty"`
	AnswerNotContains []string       `json:"answer_not_contains,omitempty"`
	Sources           []ai.Source    `json:"sources"`
	Drafts            []DraftRef     `json:"drafts"`
	Reply             string         `json:"reply,omitempty"`
	FailureReasons    []string       `json:"failure_reasons,omitempty"`
	Diffs             []string       `json:"diffs,omitempty"`
}

// Metrics 是一组用例的准确率。
type Metrics struct {
	Cases                 int     `json:"cases"`
	ToolSelectionPass     int     `json:"tool_selection_pass"`
	ToolSelectionAccuracy float64 `json:"tool_selection_accuracy"`
	ArgumentCallPass      int     `json:"argument_call_pass"`
	ArgumentCallTotal     int     `json:"argument_call_total"`
	ArgumentCallAccuracy  float64 `json:"argument_call_accuracy"`
	ArgumentCasePass      int     `json:"argument_case_pass"`
	ArgumentCaseAccuracy  float64 `json:"argument_case_accuracy"`
	AnswerPass            int     `json:"answer_pass"`
	AnswerAccuracy        float64 `json:"answer_accuracy"`
	ToolSuccessPass       int     `json:"tool_success_pass"`
	ToolSuccessAccuracy   float64 `json:"tool_success_accuracy"`
	OverallPass           int     `json:"overall_pass"`
	OverallAccuracy       float64 `json:"overall_accuracy"`
}

// Aggregates 是总体、按故事和按类别的准确率。
type Aggregates struct {
	Overall    Metrics            `json:"overall"`
	ByStory    map[string]Metrics `json:"by_story"`
	ByCategory map[string]Metrics `json:"by_category"`
}

// RunMeta 是一次评测的元数据，绝不包含 API 密钥或上游完整路径。
type RunMeta struct {
	Model        string    `json:"model"`
	BaseURLHost  string    `json:"base_url_host"`
	PromptHash   string    `json:"prompt_hash"`
	GitCommit    string    `json:"git_commit,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
	CaseFileHash string    `json:"case_file_hash"`
	CaseCount    int       `json:"case_count"`
}

// Report 是一次评测的完整 JSON 报告。
type Report struct {
	Run        RunMeta      `json:"run"`
	Cases      []CaseResult `json:"cases"`
	Aggregates Aggregates   `json:"aggregates"`
	KnownGaps  Aggregates   `json:"known_gaps"`
}
