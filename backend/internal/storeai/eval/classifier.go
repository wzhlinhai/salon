package storeaieval

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

// ClassifierModelFactory 为一条分类器用例返回模型；oracle、回放和真实模型都通过它接入。
type ClassifierModelFactory func(c storeai.ClassifierCase, refs map[string]string) ai.Model

// ClassifierCaseResult 是一条分类器用例的结果。
type ClassifierCaseResult struct {
	ID               string `json:"id"`
	Description      string `json:"description"`
	ExpectedDecision string `json:"expected_decision"`
	ActualDecision   string `json:"actual_decision"`
	ExpectedSource   string `json:"expected_source"`
	ActualSource     string `json:"actual_source"`
	ExpectedReason   string `json:"expected_reason,omitempty"`
	Reason           string `json:"reason,omitempty"`
	ModelCalls       int    `json:"model_calls"`
	Hit              bool   `json:"hit"`
	Error            string `json:"error,omitempty"`
}

// ClassifierReport 是一整套分类器评测的结果。
type ClassifierReport struct {
	Model        string                 `json:"model"`
	BaseURLHost  string                 `json:"base_url_host"`
	CaseFileHash string                 `json:"case_file_hash"`
	Total        int                    `json:"total"`
	Pass         int                    `json:"pass"`
	Accuracy     float64                `json:"accuracy"`
	Cases        []ClassifierCaseResult `json:"cases"`
}

// RunClassifierSuite 为每条用例新建隔离门店并跑一次分类。
func RunClassifierSuite(ctx context.Context, tb testing.TB, suite *storeai.ClassifierSuite, modelName, baseURLHost string, factory ClassifierModelFactory) (ClassifierReport, error) {
	report := ClassifierReport{Model: modelName, BaseURLHost: baseURLHost, CaseFileHash: suite.Hash, Total: len(suite.Cases)}
	for _, testCase := range suite.Cases {
		child := &caseTB{TB: tb}
		fixture := seedClassifier(child)
		result := runClassifierCase(ctx, child, fixture, testCase, factory)
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

func runClassifierCase(ctx context.Context, tb testing.TB, f *eval.Fixture, testCase storeai.ClassifierCase, factory ClassifierModelFactory) ClassifierCaseResult {
	tb.Helper()
	result := ClassifierCaseResult{
		ID: testCase.ID, Description: testCase.Description,
		ExpectedDecision: testCase.ExpectedDecision, ExpectedSource: testCase.ExpectedSource,
		ExpectedReason: testCase.ExpectedReason,
	}
	resolvedArgs, err := resolveValue(testCase.Args, f)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	args, err := json.Marshal(resolvedArgs)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var decoded struct {
		SubjectKind string    `json:"subject_kind"`
		SubjectID   uuid.UUID `json:"subject_id"`
		Minutes     int       `json:"minutes"`
	}
	_ = json.Unmarshal(args, &decoded)
	subject := storeai.Subject{Kind: decoded.SubjectKind, ID: decoded.SubjectID}
	var dueAt *time.Time
	if decoded.Minutes > 0 {
		due := f.Now.Add(time.Duration(decoded.Minutes) * time.Minute)
		dueAt = &due
	}
	recipient, err := classifierRecipient(tb, f, testCase)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	refs := cloneRefs(f.Refs)
	if recipient != uuid.Nil {
		refs["case:recipient"] = recipient.String()
	}
	if !subject.Empty() {
		refs["case:subject"] = subject.ID.String()
	}
	insertClassifierReminders(tb, f, recipient, subject.ID, testCase.ExistingReminder)

	var facts *storeai.ClassifyFacts
	if testCase.Object != nil {
		facts = &storeai.ClassifyFacts{
			Platform: testCase.Object.Platform, RedeemedAt: testCase.Object.RedeemedAt,
			RevocableUntil: testCase.Object.RevocableUntil, Linked: testCase.Object.Linked,
		}
	}

	var inbox dbgen.AiInbox
	if err := db.InTenant(ctx, f.Shop.Pool, f.Shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = storeai.EnsureInbox(ctx, q, f.Shop.Res.StoreID, nil)
		return err
	}); err != nil {
		result.Error = err.Error()
		return result
	}

	model := factory(testCase, refs)
	verdict, err := storeai.Classify(ctx, func(ctx context.Context, fn func(*dbgen.Queries) error) error {
		return db.InTenant(ctx, f.Shop.Pool, f.Shop.Res.TenantID, fn)
	}, model, storeai.ClassifyInput{
		TenantID: f.Shop.Res.TenantID, StoreID: f.Shop.Res.StoreID, InboxID: inbox.ID,
		Tool: testCase.Tool, Args: args, Now: f.Now, RecipientID: recipient, Subject: subject, DueAt: dueAt, Facts: facts,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.ActualDecision, result.ActualSource, result.Reason, result.ModelCalls = verdict.Decision, verdict.Source, verdict.Reason, verdict.ModelCalls
	wantCalls := 0
	if testCase.ExpectedSource == storeai.ClassifierModel {
		wantCalls = 1
	}
	result.Hit = verdict.Decision == testCase.ExpectedDecision &&
		verdict.Source == testCase.ExpectedSource &&
		(testCase.ExpectedReason == "" || verdict.Reason == testCase.ExpectedReason) &&
		verdict.ModelCalls == wantCalls
	return result
}

func classifierRecipient(tb testing.TB, f *eval.Fixture, testCase storeai.ClassifierCase) (uuid.UUID, error) {
	tb.Helper()
	switch testCase.Recipient {
	case "":
		return uuid.Nil, nil
	case "missing_staff":
		return uuid.New(), nil
	case "inactive_staff":
		shop := f.Shop
		staffID, _ := shop.Hire("停用员工", "099", "inactive-eval", shop.Role("发型师"))
		shop.Call(tb, shop.Owner, "deactivate_staff", map[string]any{"staff_id": staffID}, 200)
		return staffID, nil
	default:
		resolved, err := f.ResolveString(testCase.Recipient)
		if err != nil {
			return uuid.Nil, err
		}
		id, err := uuid.Parse(resolved)
		if err != nil {
			return uuid.Nil, fmt.Errorf("收件人 %q 不是 UUID：%w", testCase.Recipient, err)
		}
		return id, nil
	}
}

// OracleClassifier 按用例的期望回答；rule 用例不会调用它。
type OracleClassifier struct {
	Case storeai.ClassifierCase
}

func (m *OracleClassifier) Chat(_ context.Context, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
	if m.Case.ExpectedDecision == storeai.ClassifierAllow {
		return ai.Message{Role: ai.RoleAssistant, Content: `{"decision":"allow"}`}, nil
	}
	reason := m.Case.ExpectedReason
	if reason == "" {
		reason = "分类器拦下"
	}
	raw, _ := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	return ai.Message{Role: ai.RoleAssistant, Content: string(raw)}, nil
}

func cloneRefs(refs map[string]string) map[string]string {
	out := make(map[string]string, len(refs))
	for key, value := range refs {
		out[key] = value
	}
	return out
}
