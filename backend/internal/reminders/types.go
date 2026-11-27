// Package reminders 按门店定时检查提醒规则，并把选中的事写成 AI 提醒（ADR-0012）。
package reminders

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// Mode 是提醒规则的档位。
type Mode string

const (
	// ModeOff 关闭这条规则，不检查也不记录。
	ModeOff Mode = "off"
	// ModeObserve 只记下本来会提醒谁、说什么，不打扰任何人。
	ModeObserve Mode = "observe"
	// ModeOn 按收件人权限正常发出提醒。
	ModeOn Mode = "on"
)

// 检查记录的状态。
const (
	runNoMatch = "no_match"
	runDone    = "done"
	runError   = "error"
)

// 提醒的结局：发出的和拦下的都写一行。
const (
	outcomeEmitted  = "emitted"
	outcomeDeduped  = "deduped"
	outcomeCooldown = "cooldown"
	outcomeMaxTimes = "max_times"
	outcomeExpired  = "expired"
	outcomeDailyCap = "daily_cap"
)

// maxRuleFailures 是同一天一条规则连续失败多少次后不再重试。
const maxRuleFailures = 3

// Match 是规则选中的一个对象和一位收件人。
type Match struct {
	RecipientStaffID uuid.UUID
	SubjectKey       string
	// SubjectDate 是对象所属的营业日，用于过期检查；没有日期时为零值。
	SubjectDate time.Time
	// Urgent 表示不马上处理会损失钱，不占每日上限。
	Urgent bool
	Facts  any
	// FallbackText 是模型写话失败或没配模型时给收件人看的固定文案。
	FallbackText string
}

// Rule 是一条写死的提醒检查。
type Rule struct {
	Key         string
	Name        string
	Description string
	Version     int
	DefaultMode Mode
	// CheckAt 是门店当地每天检查的时刻，HH:MM。
	CheckAt string
	// Cooldown 是同一对象两次提醒之间至少要隔几个营业日，0 表示不限制。
	Cooldown int
	// MaxTimes 是同一对象最多提醒几次，0 表示不限制。
	MaxTimes int
	// MaxAge 是对象日期早于营业日多少天后不再提醒；负数表示不限制。
	MaxAge int
	// Evaluate 只读地检查规则，返回 0..n 个选中。
	Evaluate func(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, businessDate, now time.Time) ([]Match, error)
}

// ReminderRuleView 是管理后台看到的一条提醒规则。
type ReminderRuleView struct {
	RuleKey     string `json:"rule_key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode" enum:"off,observe,on"`
	IsDefault   bool   `json:"is_default" doc:"门店没有设置过，使用规则在代码里的默认档位"`
	CheckAt     string `json:"check_at" doc:"门店当地每天检查的时刻 HH:MM"`
	Version     int    `json:"version"`
}

// ReminderRuleList 是全部已注册规则。
type ReminderRuleList struct {
	Rules []ReminderRuleView `json:"rules"`
}

// SetReminderRuleModeInput 是修改一条规则档位的参数。
type SetReminderRuleModeInput struct {
	RuleKey string `json:"rule_key" minLength:"1"`
	Mode    string `json:"mode" enum:"off,observe,on"`
}

// GetReminderSummaryInput 是查看一条规则观察汇总的参数。
type GetReminderSummaryInput struct {
	RuleKey string `json:"rule_key" minLength:"1"`
	Days    int    `json:"days,omitempty" minimum:"0" maximum:"31" doc:"最近几个营业日，默认 7"`
}

// ReminderBlockedCounts 是被各道门拦下的条数。
type ReminderBlockedCounts struct {
	Deduped  int64 `json:"deduped"`
	Cooldown int64 `json:"cooldown"`
	MaxTimes int64 `json:"max_times"`
	Expired  int64 `json:"expired"`
	DailyCap int64 `json:"daily_cap"`
}

// ReminderFeedbackCounts 是老板对 AI 提醒的评价条数。
type ReminderFeedbackCounts struct {
	Helpful    int64 `json:"helpful"`
	NotHelpful int64 `json:"not_helpful"`
}

// ReminderSample 是汇总里的一条提醒样例。
type ReminderSample struct {
	ID            uuid.UUID       `json:"id"`
	BusinessDate  string          `json:"business_date" format:"date"`
	RecipientName string          `json:"recipient_name"`
	Text          string          `json:"text" doc:"显示的话：AI 写的，或固定文案"`
	AIWritten     bool            `json:"ai_written"`
	Facts         json.RawMessage `json:"facts"`
	Feedback      *string         `json:"feedback" nullable:"true" enum:"helpful,not_helpful"`
}

// ReminderSummary 是一条提醒规则最近几个营业日的观察汇总。
type ReminderSummary struct {
	RuleKey     string `json:"rule_key"`
	RuleName    string `json:"rule_name"`
	Days        int    `json:"days"`
	DateFrom    string `json:"date_from" format:"date"`
	DateTo      string `json:"date_to" format:"date"`
	RanDays     int32  `json:"ran_days" doc:"检查跑了几天的营业日数"`
	MatchedDays int32  `json:"matched_days" doc:"规则选中过至少一个对象的营业日数"`
	ErrorDays   int32  `json:"error_days" doc:"这条规则当前仍是出错状态的营业日数"`
	// LastErrorDate 是最近一条仍为出错状态的营业日，不带错误原文。
	LastErrorDate *string `json:"last_error_date" nullable:"true" format:"date"`
	Emitted       int64   `json:"emitted" doc:"发出的提醒条数（含观察档）"`
	// AveragePerRecipientDay 是发出的提醒按“收件人×营业日”平均每天几条。
	AveragePerRecipientDay float64                `json:"average_per_recipient_day"`
	Blocked                ReminderBlockedCounts  `json:"blocked"`
	Feedback               ReminderFeedbackCounts `json:"feedback"`
	Samples                []ReminderSample       `json:"samples"`
}

// SetReminderFeedbackInput 是给一条已发出的提醒评价有用或没用。
type SetReminderFeedbackInput struct {
	ReminderID uuid.UUID `json:"reminder_id"`
	Feedback   *string   `json:"feedback" nullable:"true" enum:"helpful,not_helpful"`
}

// ReminderFeedbackResult 是评价后的状态。
type ReminderFeedbackResult struct {
	ID       uuid.UUID `json:"id"`
	Feedback *string   `json:"feedback" nullable:"true" enum:"helpful,not_helpful"`
}

type reminderRuleModeAudit struct {
	Mode string `json:"mode"`
}
