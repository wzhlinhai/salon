package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Register 声明提醒规则的档位、观察汇总和评价操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[struct{}, ReminderRuleList]{
		Name: "list_reminder_rules", Summary: "查看全部提醒规则、当前档位和检查时刻", Kind: ops.Read,
		Permission: perm.SettingsManage, AI: ops.AINone, Handle: listReminderRules,
	})
	ops.Register(r, ops.Operation[SetReminderRuleModeInput, ReminderRuleView]{
		Name: "set_reminder_rule_mode", Summary: "把一条提醒规则改成关、观察或开", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AINone, Handle: setReminderRuleMode,
	})
	ops.Register(r, ops.Operation[GetReminderSummaryInput, ReminderSummary]{
		Name: "get_reminder_summary", Summary: "查看一条提醒规则最近几个营业日的观察汇总和样例", Kind: ops.Read,
		Permission: perm.SettingsManage, AI: ops.AINone, Handle: getReminderSummary,
	})
	ops.Register(r, ops.Operation[SetReminderFeedbackInput, ReminderFeedbackResult]{
		Name: "set_reminder_feedback", Summary: "给一条已发出的 AI 提醒评价有用或没用", Kind: ops.Write,
		Permission: perm.SettingsManage, AI: ops.AINone, Handle: setReminderFeedback,
	})
}

func listReminderRules(ctx context.Context, env ops.Env, _ *struct{}) (*ReminderRuleList, error) {
	rows, err := env.Q.ListReminderRuleModes(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	modes := make(map[string]string, len(rows))
	for _, row := range rows {
		modes[row.RuleKey] = row.Mode
	}
	out := &ReminderRuleList{Rules: make([]ReminderRuleView, 0, len(registeredRules))}
	for _, rule := range registeredRules {
		mode, ok := modes[rule.Key]
		out.Rules = append(out.Rules, ruleView(rule, mode, !ok))
	}
	return out, nil
}

func setReminderRuleMode(ctx context.Context, env ops.Env, in *SetReminderRuleModeInput) (*ReminderRuleView, error) {
	rule, ok := RuleByKey(in.RuleKey)
	if !ok {
		return nil, ops.Rule("没有这条提醒规则")
	}
	if !validMode(in.Mode) {
		// rule-untested: SetReminderRuleModeInput.Mode 的 schema enum 在调用前校验；AI 路径为 AINone
		return nil, ops.Rule("档位只能是关、观察或开")
	}
	if err := env.Q.LockReminderRule(ctx, dbgen.LockReminderRuleParams{StoreID: env.Caller.StoreID, RuleKey: rule.Key}); err != nil {
		return nil, err
	}
	before := rule.defaultMode()
	existing, err := env.Q.GetReminderRule(ctx, dbgen.GetReminderRuleParams{StoreID: env.Caller.StoreID, RuleKey: rule.Key})
	isDefault := true
	if err == nil {
		before = Mode(existing.Mode)
		isDefault = false
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if before == Mode(in.Mode) {
		return new(ruleView(rule, string(before), isDefault)), nil
	}
	row, err := env.Q.UpsertReminderRule(ctx, dbgen.UpsertReminderRuleParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, RuleKey: rule.Key, Mode: in.Mode, UpdatedBy: env.Caller.StaffID,
	})
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "set_reminder_rule_mode", audit.ReminderRuleTarget(row.ID),
		reminderRuleModeAudit{Mode: string(before)}, reminderRuleModeAudit{Mode: in.Mode}); err != nil {
		return nil, err
	}
	return new(ruleView(rule, in.Mode, false)), nil
}

func getReminderSummary(ctx context.Context, env ops.Env, in *GetReminderSummaryInput) (*ReminderSummary, error) {
	rule, ok := RuleByKey(in.RuleKey)
	if !ok {
		return nil, ops.Rule("没有这条提醒规则")
	}
	days := in.Days
	if days == 0 {
		days = 7
	}
	if days < 1 || days > 31 {
		// rule-untested: GetReminderSummaryInput.Days 的 schema minimum/maximum 在调用前校验；AI 路径为 AINone
		return nil, ops.Rule("天数须为 1 到 31")
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	from := today.AddDate(0, 0, -(days - 1))
	counts, err := env.Q.ReminderSummaryCounts(ctx, dbgen.ReminderSummaryCountsParams{
		StoreID: env.Caller.StoreID, RuleKey: rule.Key, DateFrom: pgDate(from), DateTo: pgDate(today),
	})
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListReminderSamples(ctx, dbgen.ListReminderSamplesParams{
		StoreID: env.Caller.StoreID, RuleKey: rule.Key, DateFrom: pgDate(from), DateTo: pgDate(today),
	})
	if err != nil {
		return nil, err
	}
	samples := make([]ReminderSample, 0, len(rows))
	for _, row := range rows {
		samples = append(samples, ReminderSample{
			ID: row.ID, BusinessDate: row.BusinessDate.Time.Format(settings.DateLayout), RecipientName: row.RecipientName,
			Text: row.Text, AIWritten: row.AiWritten, Facts: json.RawMessage(row.Facts), Feedback: row.Feedback,
		})
	}
	average := 0.0
	if counts.RecipientDays > 0 {
		average = math.Round(float64(counts.Emitted)/float64(counts.RecipientDays)*100) / 100
	}
	out := &ReminderSummary{
		RuleKey: rule.Key, RuleName: rule.Name, Days: days,
		DateFrom: from.Format(settings.DateLayout), DateTo: today.Format(settings.DateLayout),
		RanDays: counts.RanDays, MatchedDays: counts.MatchedDays, ErrorDays: counts.ErrorDays,
		Emitted: counts.Emitted, AveragePerRecipientDay: average,
		Blocked: ReminderBlockedCounts{
			Deduped: counts.Deduped, Cooldown: counts.Cooldown, MaxTimes: counts.MaxTimes,
			Expired: counts.Expired, DailyCap: counts.DailyCap,
		},
		Feedback: ReminderFeedbackCounts{Helpful: counts.Helpful, NotHelpful: counts.NotHelpful},
		Samples:  samples,
	}
	if counts.LastErrorDate != "" {
		out.LastErrorDate = &counts.LastErrorDate
	}
	return out, nil
}

func setReminderFeedback(ctx context.Context, env ops.Env, in *SetReminderFeedbackInput) (*ReminderFeedbackResult, error) {
	if in.Feedback != nil && *in.Feedback != "helpful" && *in.Feedback != "not_helpful" {
		// rule-untested: SetReminderFeedbackInput.Feedback 的 schema enum 在调用前校验；AI 路径为 AINone
		return nil, ops.Rule("评价只能是 helpful 或 not_helpful")
	}
	// 评价是对 AI 写的话的反馈，不是业务数据，所以不写操作日志。
	if _, err := env.Q.GetReminderForFeedback(ctx, dbgen.GetReminderForFeedbackParams{ID: in.ReminderID, StoreID: env.Caller.StoreID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		return nil, err
	}
	var feedbackBy *uuid.UUID
	feedbackAt := pgtype.Timestamptz{}
	if in.Feedback != nil {
		feedbackBy = &env.Caller.StaffID
		feedbackAt = pgtype.Timestamptz{Time: env.Now, Valid: true}
	}
	rows, err := env.Q.SetReminderFeedback(ctx, dbgen.SetReminderFeedbackParams{
		Feedback: in.Feedback, FeedbackBy: feedbackBy, FeedbackAt: feedbackAt, ID: in.ReminderID, StoreID: env.Caller.StoreID,
	})
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, ops.ErrNotFound
	}
	return &ReminderFeedbackResult{ID: in.ReminderID, Feedback: in.Feedback}, nil
}

func ruleView(rule Rule, mode string, isDefault bool) ReminderRuleView {
	if mode == "" {
		mode = string(rule.defaultMode())
	}
	return ReminderRuleView{
		RuleKey: rule.Key, Name: rule.Name, Description: rule.Description,
		Mode: mode, IsDefault: isDefault, CheckAt: rule.CheckAt, Version: rule.Version,
	}
}

func validMode(mode string) bool {
	return mode == string(ModeOff) || mode == string(ModeObserve) || mode == string(ModeOn)
}
