package storeai

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

//go:embed testdata/classifier_cases.json
var classifierCaseFile embed.FS

//go:embed testdata/agent_cases.json
var agentCaseFile embed.FS

// ClassifierCaseObject 是评测用例里团购券的结构化事实；时刻可以是 null。
type ClassifierCaseObject struct {
	Platform       string     `json:"platform"`
	RedeemedAt     *time.Time `json:"redeemed_at"`
	RevocableUntil *time.Time `json:"revocable_until"`
	Linked         bool       `json:"linked"`
}

// ClassifierCaseReminder 是一条已有提醒，评测时写进 ai_outbox。
type ClassifierCaseReminder struct {
	Tier   string    `json:"tier"`
	Body   string    `json:"body"`
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

// ClassifierCase 是一条分类器评测用例。
type ClassifierCase struct {
	ID               string                   `json:"id"`
	Description      string                   `json:"description"`
	Tool             string                   `json:"tool"`
	Args             json.RawMessage          `json:"args"`
	Recipient        string                   `json:"recipient,omitempty"`
	Object           *ClassifierCaseObject    `json:"object,omitempty"`
	ExistingReminder []ClassifierCaseReminder `json:"existing_reminders,omitempty"`
	ExpectedDecision string                   `json:"expected_decision"`
	ExpectedSource   string                   `json:"expected_source"`
	ExpectedReason   string                   `json:"expected_reason,omitempty"`
}

// ClassifierSuite 是分类器用例文件和它的内容哈希。
type ClassifierSuite struct {
	Cases []ClassifierCase
	Hash  string
	Raw   []byte
}

// LoadClassifierSuite 读取嵌入的分类器用例并校验。
func LoadClassifierSuite() (*ClassifierSuite, error) {
	raw, err := classifierCaseFile.ReadFile("testdata/classifier_cases.json")
	if err != nil {
		return nil, err
	}
	var cases []ClassifierCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		return nil, fmt.Errorf("parse classifier_cases.json: %w", err)
	}
	if len(cases) < 16 {
		return nil, fmt.Errorf("分类器评测用例至少 16 条，当前 %d 条", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] {
			return nil, fmt.Errorf("分类器用例 id 为空或重复：%q", c.ID)
		}
		seen[c.ID] = true
		if !classifierTools[c.Tool] {
			return nil, fmt.Errorf("%s: 工具 %q 没有标明要过分类器", c.ID, c.Tool)
		}
		if c.ExpectedDecision != ClassifierAllow && c.ExpectedDecision != ClassifierBlock {
			return nil, fmt.Errorf("%s: expected_decision 只能是 allow 或 block", c.ID)
		}
		if c.ExpectedSource != ClassifierRule && c.ExpectedSource != ClassifierModel {
			return nil, fmt.Errorf("%s: expected_source 只能是 rule 或 model", c.ID)
		}
		if c.ExpectedSource == ClassifierRule && c.ExpectedDecision == ClassifierBlock && strings.TrimSpace(c.ExpectedReason) == "" {
			return nil, fmt.Errorf("%s: rule block 用例要写 expected_reason", c.ID)
		}
		if c.Tool == "schedule_follow_up" && c.ExpectedSource != ClassifierRule {
			return nil, fmt.Errorf("%s: schedule_follow_up 只跑代码规则", c.ID)
		}
		if len(c.Args) == 0 || !json.Valid(c.Args) {
			return nil, fmt.Errorf("%s: args 不是合法 JSON", c.ID)
		}
	}
	sum := sha256.Sum256(raw)
	return &ClassifierSuite{Cases: cases, Hash: hex.EncodeToString(sum[:]), Raw: raw}, nil
}

// AgentCaseEvent 是给收件箱的一条结构化事件；payload 里的字符串可以是 fixture 符号引用。
type AgentCaseEvent struct {
	Kind      string         `json:"kind"`
	SourceKey string         `json:"source_key"`
	Subject   string         `json:"subject,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// AgentExpectedCall 是一条期望的工具调用，Args 是参数子集，字符串可以是 fixture 符号引用。
type AgentExpectedCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// AgentCase 是一条门店运行评测用例。
type AgentCase struct {
	ID            string              `json:"id"`
	Description   string              `json:"description"`
	Inbox         string              `json:"inbox"`
	Events        []AgentCaseEvent    `json:"events"`
	ExpectedCalls []AgentExpectedCall `json:"expected_calls"`
}

// AgentSuite 是门店运行用例文件和它的内容哈希。
type AgentSuite struct {
	Cases []AgentCase
	Hash  string
	Raw   []byte
}

// LoadAgentSuite 读取嵌入的门店运行评测用例并校验。
func LoadAgentSuite() (*AgentSuite, error) {
	raw, err := agentCaseFile.ReadFile("testdata/agent_cases.json")
	if err != nil {
		return nil, err
	}
	var cases []AgentCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		return nil, fmt.Errorf("parse agent_cases.json: %w", err)
	}
	if len(cases) < 6 {
		return nil, fmt.Errorf("门店运行评测用例至少 6 条，当前 %d 条", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] {
			return nil, fmt.Errorf("门店运行用例 id 为空或重复：%q", c.ID)
		}
		seen[c.ID] = true
		if c.Inbox != "store" && !strings.HasPrefix(c.Inbox, "staff:") {
			return nil, fmt.Errorf("%s: inbox 只能是 store 或 staff:<符号>", c.ID)
		}
		if len(c.Events) == 0 {
			return nil, fmt.Errorf("%s: 至少一条事件", c.ID)
		}
		for _, event := range c.Events {
			if strings.TrimSpace(event.Kind) == "" || strings.TrimSpace(event.SourceKey) == "" {
				return nil, fmt.Errorf("%s: 事件 kind 和 source_key 不能为空", c.ID)
			}
		}
		if len(c.ExpectedCalls) == 0 {
			return nil, fmt.Errorf("%s: 至少一条期望工具调用", c.ID)
		}
		for _, call := range c.ExpectedCalls {
			if strings.TrimSpace(call.Tool) == "" {
				return nil, fmt.Errorf("%s: 期望工具名不能为空", c.ID)
			}
		}
	}
	sum := sha256.Sum256(raw)
	return &AgentSuite{Cases: cases, Hash: hex.EncodeToString(sum[:]), Raw: raw}, nil
}
