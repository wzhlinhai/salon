package reminders_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClosingBriefNoBusinessNoMatch(t *testing.T) {
	f := open(t)
	now := f.now("2026-11-27", 10, 0)
	model := &fakeModel{reply: "不应调用"}
	runDefault(t, f.Shop, now, model)
	run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27")
	if run == nil || run.Status != "no_match" || run.Matched != 0 {
		t.Fatalf("run = %+v", run)
	}
	if len(allReminders(t, f.Shop)) != 0 || model.callCount() != 0 {
		t.Fatal("empty day notified or called model")
	}
}

func TestClosingBriefNotClosed(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 1 {
		t.Fatalf("reminders = %d", len(rows))
	}
	row := rows[0]
	if row.RecipientStaffID != f.Res.OwnerID || row.SubjectKey != "2026-11-26" || row.Outcome != "emitted" || row.Mode != "observe" {
		t.Fatalf("reminder = %+v", row)
	}
	facts := factsOf(t, row)
	if !facts.HadBusiness || facts.Closed || facts.VoidCount != 0 || facts.Unrecorded.Count != 0 {
		t.Fatalf("facts = %+v", facts)
	}
	if row.FallbackText != "昨天（11月26日）还没日结。" {
		t.Fatalf("fallback = %q", row.FallbackText)
	}
	run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27")
	if run == nil || run.Status != "done" || run.Matched != 1 {
		t.Fatalf("run = %+v", run)
	}
}

func TestClosingBriefDifference(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	f.now("2026-11-26", 13, 0)
	f.closeDay("2026-11-26", "90.00")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 1 {
		t.Fatalf("reminders = %d", len(rows))
	}
	facts := factsOf(t, rows[0])
	if !facts.Closed || len(facts.Differences) != 1 || facts.Differences[0].Amount.String() != "-10.00" {
		t.Fatalf("facts = %+v", facts)
	}
	if !strings.Contains(rows[0].FallbackText, "现金差额 -10.00 元") {
		t.Fatalf("fallback = %q", rows[0].FallbackText)
	}
}

func TestClosingBriefClosedCleanNoMatch(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	f.now("2026-11-26", 13, 0)
	f.closeDay("2026-11-26", "100.00")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	if len(allReminders(t, f.Shop)) != 0 {
		t.Fatal("clean closed day notified")
	}
	run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27")
	if run == nil || run.Status != "no_match" {
		t.Fatalf("run = %+v", run)
	}
}

func TestClosingBriefVoidCount(t *testing.T) {
	f := open(t)
	order := f.businessDay("2026-11-26")
	f.now("2026-11-26", 13, 0)
	f.Call(t, f.Owner, "void_order", map[string]any{"order_id": order, "reason": "测试"}, 200)
	f.closeDay("2026-11-26", "0.00")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 1 || factsOf(t, rows[0]).VoidCount != 1 {
		t.Fatalf("reminders = %+v", rows)
	}
	if !strings.Contains(rows[0].FallbackText, "删单 1 笔") {
		t.Fatalf("fallback = %q", rows[0].FallbackText)
	}
}

func TestClosingBriefUnrecorded(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	cardType := uuid.MustParse(f.Call(t, f.Owner, "create_card_type", map[string]any{"name": "储值", "kind": "stored_value", "validity_kind": "none"}, 200).Body["id"].(string))
	f.now("2026-11-26", 13, 0)
	cardID := uuid.MustParse(f.Call(t, f.Owner, "open_card", map[string]any{"customer_id": f.customer, "card_type_id": cardType, "principal": "10.00", "bonus": "0.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": "10.00"}}}, 200).Body["id"].(string))
	insertUnrecordedCardSale(t, f.Shop, cardID, "2026-11-26", "50.00")
	f.closeDay("2026-11-26", "110.00")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 1 {
		t.Fatalf("reminders = %d", len(rows))
	}
	facts := factsOf(t, rows[0])
	if facts.Unrecorded.Count != 1 || facts.Unrecorded.Amount.String() != "50.00" {
		t.Fatalf("facts = %+v", facts)
	}
	if !strings.Contains(rows[0].FallbackText, "未记收款方式 1 笔，共 50.00 元") {
		t.Fatalf("fallback = %q", rows[0].FallbackText)
	}
}

func TestClosingBriefActionCounts(t *testing.T) {
	for _, action := range []string{"adjust_card", "reprice_order_item", "remove_order_item", "set_paid_item_staff"} {
		t.Run(action, func(t *testing.T) {
			f := open(t)
			f.businessDay("2026-11-26")
			f.now("2026-11-26", 13, 0)
			f.closeDay("2026-11-26", "100.00")
			insertAudit(t, f.Shop, action, at("2026-11-26", 15, 0))
			now := f.now("2026-11-27", 10, 0)
			runDefault(t, f.Shop, now, nil)
			rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
			if len(rows) != 1 {
				t.Fatalf("reminders = %d", len(rows))
			}
			facts := factsOf(t, rows[0]).Actions
			got := map[string]int{"adjust_card": facts.AdjustCard, "reprice_order_item": facts.RepriceOrderItem, "remove_order_item": facts.RemoveOrderItem, "set_paid_item_staff": facts.SetPaidItemStaff}
			if got[action] != 1 {
				t.Fatalf("actions = %+v", facts)
			}
		})
	}
}

func TestClosingBriefBusinessDayBoundary(t *testing.T) {
	f := open(t)
	f.now("2026-11-25", 12, 0)
	f.Call(t, f.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "03:00", "effective_from": "2026-11-26"}, 200)
	f.businessDay("2026-11-26")
	insertAudit(t, f.Shop, "adjust_card", at("2026-11-27", 1, 0))
	insertAudit(t, f.Shop, "reprice_order_item", at("2026-11-27", 4, 0))
	now := at("2026-11-27", 10, 0)
	f.SetNow(now)
	runDefault(t, f.Shop, now, nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 1 {
		t.Fatalf("reminders = %d", len(rows))
	}
	facts := factsOf(t, rows[0])
	if facts.Actions.AdjustCard != 1 || facts.Actions.RepriceOrderItem != 0 {
		t.Fatalf("actions = %+v", facts.Actions)
	}
}

func TestClosingBriefCheckTime(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	early := f.now("2026-11-27", 9, 29)
	runDefault(t, f.Shop, early, nil)
	if run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27"); run != nil {
		t.Fatalf("ran before check time: %+v", run)
	}
	late := f.now("2026-11-27", 9, 30)
	runDefault(t, f.Shop, late, nil)
	if run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27"); run == nil || run.Status != "done" {
		t.Fatalf("run = %+v", run)
	}
}

func TestClosingBriefRecipients(t *testing.T) {
	f := open(t)
	managerID, _ := f.Hire("店长", "002", "manager", f.Role("店长"))
	stylistID, _ := f.Hire("发型师", "003", "stylist", f.Role("发型师"))
	inactiveID, _ := f.Hire("停用店长", "004", "inactive", f.Role("店长"))
	f.Call(t, f.Owner, "deactivate_staff", map[string]any{"staff_id": inactiveID}, 200)
	f.businessDay("2026-11-26")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	got := map[uuid.UUID]bool{}
	for _, row := range remindersByRule(t, f.Shop, "closing_yesterday_brief") {
		got[row.RecipientStaffID] = true
	}
	if !got[f.Res.OwnerID] || !got[managerID] || got[stylistID] || got[inactiveID] {
		t.Fatalf("recipients = %v", got)
	}
}

func TestClosingBriefSameDayAndRestartNoDuplicate(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	runDefault(t, f.Shop, now, nil)
	if len(remindersByRule(t, f.Shop, "closing_yesterday_brief")) != 1 {
		t.Fatal("same day ran twice")
	}
	if run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27"); run == nil || run.Status != "done" {
		t.Fatalf("run = %+v", run)
	}
}

func TestClosingBriefDoesNotBackfillYesterday(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-25")
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	if run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-25"); run != nil {
		t.Fatalf("backfilled yesterday: %+v", run)
	}
	if len(remindersByRule(t, f.Shop, "closing_yesterday_brief")) != 0 {
		t.Fatal("backfilled reminder")
	}
}

func TestClosingBriefFallbackListsOnlyPresentItems(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	f.now("2026-11-26", 13, 0)
	f.closeDay("2026-11-26", "100.00")
	insertAudit(t, f.Shop, "adjust_card", at("2026-11-26", 14, 0))
	insertAudit(t, f.Shop, "reprice_order_item", at("2026-11-26", 14, 1))
	insertAudit(t, f.Shop, "reprice_order_item", at("2026-11-26", 14, 2))
	now := f.now("2026-11-27", 10, 0)
	runDefault(t, f.Shop, now, nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 1 {
		t.Fatalf("reminders = %d", len(rows))
	}
	want := "昨天（11月26日）已日结。手工调卡 1 次，改价 2 次。"
	if rows[0].FallbackText != want {
		t.Fatalf("fallback = %q", rows[0].FallbackText)
	}
}
