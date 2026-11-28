package storeaieval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

// AgentModelFactory 为一条门店运行用例返回模型。
type AgentModelFactory func(c storeai.AgentCase, f *eval.Fixture) ai.Model

// AgentCall 是报告里一次工具调用。Error 非空表示这次调用的工具结果不是成功。
type AgentCall struct {
	Tool  string         `json:"tool"`
	Args  map[string]any `json:"args,omitempty"`
	Error string         `json:"error,omitempty"`
}

// AgentCaseResult 是一条门店运行用例的结果。
type AgentCaseResult struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Expected    []AgentCall `json:"expected"`
	Actual      []AgentCall `json:"actual"`
	Hit         bool        `json:"hit"`
	Error       string      `json:"error,omitempty"`
}

// AgentReport 是一整套门店运行评测的结果。
type AgentReport struct {
	Model        string            `json:"model"`
	BaseURLHost  string            `json:"base_url_host"`
	CaseFileHash string            `json:"case_file_hash"`
	Total        int               `json:"total"`
	Pass         int               `json:"pass"`
	Accuracy     float64           `json:"accuracy"`
	Cases        []AgentCaseResult `json:"cases"`
}

// RunAgentSuite 为每条用例新建隔离门店并跑一次收件箱事件批。
func RunAgentSuite(ctx context.Context, tb testing.TB, suite *storeai.AgentSuite, modelName, baseURLHost string, factory AgentModelFactory) (AgentReport, error) {
	report := AgentReport{Model: modelName, BaseURLHost: baseURLHost, CaseFileHash: suite.Hash, Total: len(suite.Cases)}
	for _, testCase := range suite.Cases {
		child := &caseTB{TB: tb}
		fixture := seedAgent(child)
		result := runAgentCase(ctx, child, fixture, testCase, factory)
		child.runCleanups()
		report.Cases = append(report.Cases, result)
		if result.Hit {
			report.Pass++
		}
	}
	if report.Total > 0 {
		report.Accuracy = float64(report.Pass) / float64(report.Total)
	}
	return report, nil
}

func runAgentCase(ctx context.Context, tb testing.TB, f *eval.Fixture, testCase storeai.AgentCase, factory AgentModelFactory) AgentCaseResult {
	tb.Helper()
	result := AgentCaseResult{ID: testCase.ID, Description: testCase.Description}
	expected, err := resolvedExpectedCalls(f, testCase.ExpectedCalls)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Expected = expected
	inbox, err := agentInbox(ctx, f, testCase.Inbox)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	sourceKeys := make([]string, 0, len(testCase.Events))
	var firstSubject storeai.Subject
	for i, event := range testCase.Events {
		subject, err := resolveSubject(f, event.Subject)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		if i == 0 {
			firstSubject = subject
		}
		payload := map[string]any{}
		if event.Payload != nil {
			resolved, err := resolveValue(event.Payload, f)
			if err != nil {
				result.Error = err.Error()
				return result
			}
			payload = resolved.(map[string]any)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		sourceKey := replaceRefs(event.SourceKey, f.Refs)
		sourceKeys = append(sourceKeys, sourceKey)
		if err := db.InTenant(ctx, f.Shop.Pool, f.Shop.Res.TenantID, func(q *dbgen.Queries) error {
			_, err := storeai.Emit(ctx, q, f.Now, storeai.EmitInput{
				TenantID: f.Shop.Res.TenantID, StoreID: f.Shop.Res.StoreID, InboxID: inbox.ID,
				Kind: event.Kind, SourceKey: sourceKey, Subject: subject, Payload: body,
			})
			return err
		}); err != nil {
			result.Error = err.Error()
			return result
		}
	}

	var session dbgen.AiSession
	var events []dbgen.AiInboxEvent
	if err := db.InTenant(ctx, f.Shop.Pool, f.Shop.Res.TenantID, func(q *dbgen.Queries) error {
		all, err := q.ListInboxEvents(ctx, inbox.ID)
		if err != nil {
			return err
		}
		wanted := map[string]bool{}
		for _, key := range sourceKeys {
			wanted[key] = true
		}
		for _, event := range all {
			if wanted[event.SourceKey] {
				events = append(events, event)
			}
		}
		var subjectKind *string
		var subjectID *uuid.UUID
		if !firstSubject.Empty() {
			kind, id := firstSubject.Kind, firstSubject.ID
			subjectKind, subjectID = &kind, &id
		}
		session, err = q.CreateSession(ctx, dbgen.CreateSessionParams{
			TenantID: f.Shop.Res.TenantID, StoreID: f.Shop.Res.StoreID, InboxID: inbox.ID,
			SubjectKind: subjectKind, SubjectID: subjectID, Now: timestamp(f.Now),
		})
		return err
	}); err != nil {
		result.Error = err.Error()
		return result
	}

	runner := &storeai.Runner{
		Registry: f.Shop.Registry, Model: func() ai.Model { return factory(testCase, f) },
		Classifier: func() ai.Model { return agentAllowClassifier{} }, Logger: evalDiscardLogger(),
	}
	runResult, err := runner.Run(ctx, storeai.RunInput{
		TenantID: f.Shop.Res.TenantID, StoreID: f.Shop.Res.StoreID, Inbox: inbox, Session: session,
		Events: events, Now: f.Now,
		Tx: func(ctx context.Context, fn func(*dbgen.Queries) error) error {
			return db.InTenant(ctx, f.Shop.Pool, f.Shop.Res.TenantID, fn)
		},
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	actual, err := observedCalls(runResult.Messages)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Actual = actual
	result.Hit = matchAgentCalls(expected, actual)
	return result
}

func agentInbox(ctx context.Context, f *eval.Fixture, name string) (dbgen.AiInbox, error) {
	var staffID *uuid.UUID
	if strings.HasPrefix(name, "staff:") {
		resolved, err := f.ResolveString(name)
		if err != nil {
			return dbgen.AiInbox{}, err
		}
		id, err := uuid.Parse(resolved)
		if err != nil {
			return dbgen.AiInbox{}, fmt.Errorf("收件箱 %q 不是 UUID：%w", name, err)
		}
		staffID = &id
	}
	var inbox dbgen.AiInbox
	err := db.InTenant(ctx, f.Shop.Pool, f.Shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = storeai.EnsureInbox(ctx, q, f.Shop.Res.StoreID, staffID)
		return err
	})
	return inbox, err
}

func resolveSubject(f *eval.Fixture, value string) (storeai.Subject, error) {
	if strings.TrimSpace(value) == "" {
		return storeai.Subject{}, nil
	}
	resolved, err := f.ResolveString(value)
	if err != nil {
		return storeai.Subject{}, err
	}
	id, err := uuid.Parse(resolved)
	if err != nil {
		return storeai.Subject{}, fmt.Errorf("对象 %q 不是 UUID：%w", value, err)
	}
	return storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}, nil
}

func resolvedExpectedCalls(f *eval.Fixture, calls []storeai.AgentExpectedCall) ([]AgentCall, error) {
	out := make([]AgentCall, 0, len(calls))
	for _, call := range calls {
		args := map[string]any{}
		if call.Args != nil {
			resolved, err := resolveValue(call.Args, f)
			if err != nil {
				return nil, err
			}
			args = resolved.(map[string]any)
		}
		out = append(out, AgentCall{Tool: call.Tool, Args: args})
	}
	return out, nil
}

// agentWriteTools 是门店 AI 自己的写工具；评测只按顺序比对这些工具，
// 中间夹的只读调用（Registry 的 Read + AIDirect）忽略。
var agentWriteTools = map[string]bool{
	"schedule_follow_up": true,
	"create_request":     true,
	"hand_over":          true,
	"cancel_request":     true,
	"remind_me":          true,
	"finish":             true,
}

func observedCalls(raw json.RawMessage) ([]AgentCall, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var messages []ai.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	results := map[string]string{}
	for _, message := range messages {
		if message.Role == ai.RoleTool && message.ToolCallID != "" {
			results[message.ToolCallID] = message.Content
		}
	}
	out := []AgentCall{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if !agentWriteTools[call.Name] {
				continue
			}
			args := map[string]any{}
			if strings.TrimSpace(call.Arguments) != "" {
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
					return nil, fmt.Errorf("工具 %s 参数不是 JSON：%w", call.Name, err)
				}
			}
			item := AgentCall{Tool: call.Name, Args: args}
			content, ok := results[call.ID]
			if !ok {
				item.Error = "工具没有结果"
			} else if !json.Valid([]byte(content)) {
				item.Error = content
			}
			out = append(out, item)
		}
	}
	return out, nil
}

func matchAgentCalls(expected, actual []AgentCall) bool {
	expected = agentWriteCalls(expected)
	if len(expected) == 0 {
		return len(actual) == 0
	}
	if expected[len(expected)-1].Tool != "finish" {
		for len(actual) > 0 && actual[len(actual)-1].Tool == "finish" {
			actual = actual[:len(actual)-1]
		}
	}
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i].Tool != actual[i].Tool || !argsSubset(actual[i].Args, expected[i].Args) || actual[i].Error != "" {
			return false
		}
	}
	return true
}

func agentWriteCalls(calls []AgentCall) []AgentCall {
	out := make([]AgentCall, 0, len(calls))
	for _, call := range calls {
		if agentWriteTools[call.Tool] {
			out = append(out, call)
		}
	}
	return out
}

func argsSubset(actual, expected any) bool {
	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range want {
			if !argsSubset(got[key], value) {
				return false
			}
		}
		return true
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if !argsSubset(got[i], want[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(actual, expected)
	}
}

func replaceRefs(text string, refs map[string]string) string {
	for key, value := range refs {
		text = strings.ReplaceAll(text, key, value)
	}
	return text
}

// OracleAgent 按期望调用序列发工具调用，最后给出结束语。
type OracleAgent struct {
	Case    storeai.AgentCase
	Fixture *eval.Fixture
	call    int
}

func (m *OracleAgent) Chat(_ context.Context, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
	if m.call >= len(m.Case.ExpectedCalls) {
		return ai.Message{Role: ai.RoleAssistant, Content: "完成"}, nil
	}
	expected := m.Case.ExpectedCalls[m.call]
	args := map[string]any{}
	if expected.Args != nil {
		resolved, err := resolveValue(expected.Args, m.Fixture)
		if err != nil {
			return ai.Message{}, err
		}
		args = resolved.(map[string]any)
	}
	if expected.Tool == "remind_me" {
		if _, ok := args["body"]; !ok {
			args["body"] = "这张券还没关联单据"
		}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ai.Message{}, err
	}
	m.call++
	return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{
		ID: fmt.Sprintf("oracle_%d", m.call), Name: expected.Tool, Arguments: string(raw),
	}}}, nil
}

type agentAllowClassifier struct{}

func (agentAllowClassifier) Chat(context.Context, []ai.Message, []ops.Tool) (ai.Message, error) {
	return ai.Message{Role: ai.RoleAssistant, Content: `{"decision":"allow"}`}, nil
}

func evalDiscardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
