package reminders_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/reminders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

type fixture struct {
	*optest.Shop
	t        *testing.T
	item     uuid.UUID
	customer uuid.UUID
}

func open(t *testing.T) *fixture {
	t.Helper()
	s := optest.OpenShop(t)
	f := &fixture{Shop: s, t: t}
	r := s.Call(t, s.Owner, "create_item", map[string]any{"name": "剪发", "price": "100.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200)
	f.item = uuid.MustParse(r.Body["id"].(string))
	r = s.Call(t, s.Owner, "create_customer", map[string]any{"name": "顾客", "phone": "13800138000"}, 200)
	f.customer = uuid.MustParse(r.Body["customer"].(map[string]any)["id"].(string))
	return f
}

func at(day string, hour, minute int) time.Time {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, settings.Location)
}

func (f *fixture) now(day string, hour, minute int) time.Time {
	f.t.Helper()
	t := at(day, hour, minute)
	f.SetNow(t)
	return t
}

func (f *fixture) order(day string, price string) uuid.UUID {
	f.t.Helper()
	f.now(day, 12, 0)
	r := f.Call(f.t, f.Owner, "open_order", map[string]any{"customer_id": f.customer, "gender": "female", "designated": false, "items": []any{map[string]any{"item_id": f.item, "staff": []any{}}}}, 200)
	id := uuid.MustParse(r.Body["id"].(string))
	f.Call(f.t, f.Owner, "reprice_order_item", map[string]any{"order_id": id, "order_item_id": r.Body["items"].([]any)[0].(map[string]any)["id"], "price": price, "reason": "测试金额"}, 200)
	return id
}

func (f *fixture) checkout(order uuid.UUID) {
	f.t.Helper()
	f.Call(f.t, f.Owner, "checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order, "payments": []any{map[string]any{"method": "cash", "amount": "100.00"}}}}}, 200)
}

func (f *fixture) closeDay(day, cash string) {
	f.t.Helper()
	d, err := time.Parse(settings.DateLayout, day)
	if err != nil {
		f.t.Fatal(err)
	}
	// 日结只能结今天以前的营业日，把当前时刻放到次日早上再结。
	f.SetNow(time.Date(d.Year(), d.Month(), d.Day()+1, 8, 0, 0, 0, settings.Location))
	f.Call(f.t, f.Owner, "close_day", map[string]any{
		"date": day, "cash_counted": cash, "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0", "note": nil,
	}, 200)
}

func (f *fixture) businessDay(day string) uuid.UUID {
	f.t.Helper()
	order := f.order(day, "100.00")
	f.checkout(order)
	return order
}

func runDefault(t *testing.T, s *optest.Shop, now time.Time, model ai.Model) {
	t.Helper()
	reminders.RunOnce(t.Context(), s.Pool, now, modelFunc(model), discardLogger())
}

func runRules(t *testing.T, s *optest.Shop, now time.Time, model ai.Model, rules []reminders.Rule) {
	t.Helper()
	reminders.RunOnceWithRules(t.Context(), s.Pool, now, modelFunc(model), discardLogger(), rules)
}

func ownerCallerOf(f *fixture) *ops.Caller {
	caller := f.MustCaller(f.Owner)
	return &caller
}

func modelFunc(model ai.Model) func() ai.Model {
	if model == nil {
		return func() ai.Model { return nil }
	}
	return func() ai.Model { return model }
}

func runRow(t *testing.T, s *optest.Shop, ruleKey, day string) *dbgen.AiReminderRun {
	t.Helper()
	return runRowMode(t, s, ruleKey, day, string(reminders.ModeObserve))
}

func runRowMode(t *testing.T, s *optest.Shop, ruleKey, day, mode string) *dbgen.AiReminderRun {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	row, err := dbgen.New(tx).GetReminderRun(context.Background(), dbgen.GetReminderRunParams{
		StoreID: s.Res.StoreID, RuleKey: ruleKey, BusinessDate: pgtype.Date{Time: mustDate(day), Valid: true}, Mode: mode,
	})
	dbtest.Rollback(t, tx)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return &row
}

func allReminders(t *testing.T, s *optest.Shop) []dbgen.AiReminder {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT tenant_id,store_id,id,rule_key,rule_version,mode,recipient_staff_id,subject_key,business_date,urgent,facts,fallback_text,ai_text,outcome,feedback,feedback_by,feedback_at,created_at FROM ai_reminders WHERE store_id=$1 ORDER BY business_date,created_at,id`, s.Res.StoreID)
	if err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	out := []dbgen.AiReminder{}
	for rows.Next() {
		var row dbgen.AiReminder
		if err := rows.Scan(&row.TenantID, &row.StoreID, &row.ID, &row.RuleKey, &row.RuleVersion, &row.Mode, &row.RecipientStaffID, &row.SubjectKey, &row.BusinessDate, &row.Urgent, &row.Facts, &row.FallbackText, &row.AiText, &row.Outcome, &row.Feedback, &row.FeedbackBy, &row.FeedbackAt, &row.CreatedAt); err != nil {
			rows.Close()
			dbtest.Rollback(t, tx)
			t.Fatal(err)
		}
		out = append(out, row)
	}
	rows.Close()
	dbtest.Rollback(t, tx)
	return out
}

func remindersByRule(t *testing.T, s *optest.Shop, ruleKey string) []dbgen.AiReminder {
	t.Helper()
	out := []dbgen.AiReminder{}
	for _, row := range allReminders(t, s) {
		if row.RuleKey == ruleKey {
			out = append(out, row)
		}
	}
	return out
}

func mustDate(day string) time.Time {
	d, err := time.Parse(settings.DateLayout, day)
	if err != nil {
		panic(err)
	}
	return d
}

func insertAudit(t *testing.T, s *optest.Shop, action string, atTime time.Time) {
	t.Helper()
	insertAuditAt(t, s, s.Res.StoreID, action, atTime)
}

func insertAuditAt(t *testing.T, s *optest.Shop, storeID uuid.UUID, action string, atTime time.Time) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO audit_log (tenant_id,store_id,action,target_kind,target_id,actor_id,at) VALUES ($1,$2,$3,'shift',$4,$5,$6)`,
		s.Res.TenantID, storeID, action, uuid.New(), s.Res.OwnerID, pgtype.Timestamptz{Time: atTime, Valid: true})
	dbtest.Commit(t, tx)
}

func insertUnrecordedCardSale(t *testing.T, s *optest.Shop, cardID uuid.UUID, day string, amount string) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id)
		VALUES ($1,$2,$3,'open',0,$4,$4,0,0,0,$5,$6)`,
		s.Res.TenantID, s.Res.StoreID, cardID, amount, day, s.Res.OwnerID)
	dbtest.Commit(t, tx)
}

func factsOf(t *testing.T, row dbgen.AiReminder) reminders.ClosingBriefFacts {
	t.Helper()
	var facts reminders.ClosingBriefFacts
	if err := json.Unmarshal(row.Facts, &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func testRule(key string, matches func(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, businessDate, now time.Time) ([]reminders.Match, error)) reminders.Rule {
	return reminders.Rule{
		Key: key, Name: "测试规则", Description: "测试用规则", Version: 1,
		DefaultMode: reminders.ModeObserve, CheckAt: "09:30", MaxAge: -1,
		Evaluate: matches,
	}
}

func ownerMatch(s *optest.Shop, subject string, facts any) reminders.Match {
	return reminders.Match{RecipientStaffID: s.Res.OwnerID, SubjectKey: subject, Facts: facts, FallbackText: "固定文案"}
}

type fakeModel struct {
	mu       sync.Mutex
	reply    string
	err      error
	block    bool
	calls    int
	messages []ai.Message
}

func (m *fakeModel) Chat(ctx context.Context, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
	m.mu.Lock()
	m.calls++
	m.messages = append(m.messages, messages...)
	reply, err, block := m.reply, m.err, m.block
	m.mu.Unlock()
	if block {
		<-ctx.Done()
		return ai.Message{}, ctx.Err()
	}
	if err != nil {
		return ai.Message{}, err
	}
	return ai.Message{Role: ai.RoleAssistant, Content: reply}, nil
}

func (m *fakeModel) prompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var parts []string
	for _, message := range m.messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func (m *fakeModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}
