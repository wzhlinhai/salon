package reminders

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/closing"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

var closingYesterdayBrief = Rule{
	Key:         "closing_yesterday_brief",
	Name:        "昨日日结简报",
	Description: "昨天没日结、有对账差额或昨天有删单、调卡、改价、删项目、结账后改做的人时，第二天早上提醒有日结权限的人。",
	Version:     1,
	DefaultMode: ModeObserve,
	CheckAt:     "09:30",
	Cooldown:    0,
	MaxTimes:    1,
	MaxAge:      1,
	Evaluate:    evaluateClosingYesterdayBrief,
}

// ClosingBriefFacts 是昨日日结简报交给模型和汇总展示的事实，全部由代码计算。
type ClosingBriefFacts struct {
	BusinessDate string                   `json:"business_date"`
	HadBusiness  bool                     `json:"had_business"`
	Closed       bool                     `json:"closed"`
	Differences  []ClosingBriefDifference `json:"differences,omitempty"`
	VoidCount    int64                    `json:"void_count"`
	Unrecorded   ClosingBriefUnrecorded   `json:"unrecorded"`
	Actions      ClosingBriefActionCounts `json:"actions"`
}

// ClosingBriefDifference 是一种收款方式的不为零差额。
type ClosingBriefDifference struct {
	Method string       `json:"method"`
	Label  string       `json:"label"`
	Amount money.Change `json:"amount"`
}

// ClosingBriefUnrecorded 是未记收款方式的笔数和金额。
type ClosingBriefUnrecorded struct {
	Count  int64        `json:"count"`
	Amount money.Change `json:"amount"`
}

// ClosingBriefActionCounts 是昨天营业日内几类操作日志的次数。
type ClosingBriefActionCounts struct {
	AdjustCard       int `json:"adjust_card"`
	RepriceOrderItem int `json:"reprice_order_item"`
	RemoveOrderItem  int `json:"remove_order_item"`
	SetPaidItemStaff int `json:"set_paid_item_staff"`
}

func (c ClosingBriefActionCounts) any() bool {
	return c.AdjustCard > 0 || c.RepriceOrderItem > 0 || c.RemoveOrderItem > 0 || c.SetPaidItemStaff > 0
}

func evaluateClosingYesterdayBrief(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, businessDate, now time.Time) ([]Match, error) {
	yesterday := businessDate.AddDate(0, 0, -1)
	env := ops.Env{Now: now, Caller: ops.Caller{StoreID: storeID}, Q: q}
	days, err := closing.BusinessDaysIn(ctx, env, yesterday, yesterday)
	if err != nil {
		return nil, err
	}
	hadBusiness := len(days) > 0
	system, err := closing.SystemFiguresForDay(ctx, env, yesterday)
	if err != nil {
		return nil, err
	}
	current, closed, err := closing.CurrentDayClose(ctx, env, yesterday)
	if err != nil {
		return nil, err
	}
	var differences []ClosingBriefDifference
	if closed {
		for _, line := range current.Differences {
			if line.Difference.IsZero() {
				continue
			}
			differences = append(differences, ClosingBriefDifference{Method: line.Method, Label: line.Label, Amount: line.Difference})
		}
	}
	actions, err := closingActionCounts(ctx, q, storeID, yesterday)
	if err != nil {
		return nil, err
	}
	selected := (hadBusiness && !closed) || len(differences) > 0 || system.VoidCount > 0 || system.Unrecorded.Count > 0 || actions.any()
	if !selected {
		return nil, nil
	}
	facts := ClosingBriefFacts{
		BusinessDate: yesterday.Format(settings.DateLayout),
		HadBusiness:  hadBusiness,
		Closed:       closed,
		Differences:  differences,
		VoidCount:    system.VoidCount,
		Unrecorded:   ClosingBriefUnrecorded{Count: system.Unrecorded.Count, Amount: system.Unrecorded.Amount},
		Actions:      actions,
	}
	fallback := closingBriefFallback(facts, yesterday)
	staff, err := q.ListStaff(ctx, storeID)
	if err != nil {
		return nil, err
	}
	subject := yesterday.Format(settings.DateLayout)
	matches := []Match{}
	for _, st := range staff {
		if st.StoreID != storeID {
			continue
		}
		caller, ok, err := auth.LoadCaller(ctx, q, st.ID)
		if err != nil {
			return nil, err
		}
		if !ok || !caller.Has(perm.ClosingDaily) {
			continue
		}
		matches = append(matches, Match{
			RecipientStaffID: st.ID,
			SubjectKey:       subject,
			SubjectDate:      yesterday,
			Facts:            facts,
			FallbackText:     fallback,
		})
	}
	return matches, nil
}

func closingActionCounts(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, yesterday time.Time) (ClosingBriefActionCounts, error) {
	var out ClosingBriefActionCounts
	actions := []string{"adjust_card", "reprice_order_item", "remove_order_item", "set_paid_item_staff"}
	// 时间窗在自然日两端各放宽两天，换算后的营业日仍只取 yesterday。
	windowFrom := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, settings.Location).AddDate(0, 0, -2)
	windowTo := windowFrom.AddDate(0, 0, 5)
	rows, err := q.ReminderAuditActions(ctx, dbgen.ReminderAuditActionsParams{
		StoreID: storeID, Actions: actions,
		WindowFrom: pgtype.Timestamptz{Time: windowFrom, Valid: true},
		WindowTo:   pgtype.Timestamptz{Time: windowTo, Valid: true},
	})
	if err != nil {
		return out, err
	}
	if len(rows) == 0 {
		return out, nil
	}
	times := make([]time.Time, len(rows))
	for i, row := range rows {
		times[i] = row.At.Time
	}
	days, err := settings.BusinessDates(ctx, q, storeID, times)
	if err != nil {
		return out, err
	}
	for i, row := range rows {
		if !days[i].Equal(yesterday) {
			continue
		}
		switch row.Action {
		case "adjust_card":
			out.AdjustCard++
		case "reprice_order_item":
			out.RepriceOrderItem++
		case "remove_order_item":
			out.RemoveOrderItem++
		case "set_paid_item_staff":
			out.SetPaidItemStaff++
		}
	}
	return out, nil
}

func closingBriefFallback(facts ClosingBriefFacts, yesterday time.Time) string {
	day := fmt.Sprintf("%d月%d日", int(yesterday.Month()), yesterday.Day())
	var parts []string
	switch {
	case !facts.HadBusiness:
		parts = append(parts, fmt.Sprintf("昨天（%s）没有营业。", day))
	case !facts.Closed:
		parts = append(parts, fmt.Sprintf("昨天（%s）还没日结。", day))
	default:
		parts = append(parts, fmt.Sprintf("昨天（%s）已日结。", day))
	}
	var details []string
	for _, line := range facts.Differences {
		details = append(details, fmt.Sprintf("%s差额 %s 元", line.Label, line.Amount.Format()))
	}
	if facts.VoidCount > 0 {
		details = append(details, fmt.Sprintf("删单 %d 笔", facts.VoidCount))
	}
	if facts.Unrecorded.Count > 0 {
		details = append(details, fmt.Sprintf("未记收款方式 %d 笔，共 %s 元", facts.Unrecorded.Count, facts.Unrecorded.Amount.Format()))
	}
	if facts.Actions.AdjustCard > 0 {
		details = append(details, fmt.Sprintf("手工调卡 %d 次", facts.Actions.AdjustCard))
	}
	if facts.Actions.RepriceOrderItem > 0 {
		details = append(details, fmt.Sprintf("改价 %d 次", facts.Actions.RepriceOrderItem))
	}
	if facts.Actions.RemoveOrderItem > 0 {
		details = append(details, fmt.Sprintf("删项目 %d 次", facts.Actions.RemoveOrderItem))
	}
	if facts.Actions.SetPaidItemStaff > 0 {
		details = append(details, fmt.Sprintf("结账后改做的人 %d 次", facts.Actions.SetPaidItemStaff))
	}
	if len(details) > 0 {
		parts = append(parts, strings.Join(details, "，")+"。")
	}
	return strings.Join(parts, "")
}
