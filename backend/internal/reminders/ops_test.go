package reminders_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestReminderRuleListAndSetMode(t *testing.T) {
	f := open(t)
	r := f.Call(t, f.Owner, "list_reminder_rules", map[string]any{}, 200)
	rules := r.Body["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %v", rules)
	}
	rule := rules[0].(map[string]any)
	if rule["rule_key"] != "closing_yesterday_brief" || rule["mode"] != "observe" || rule["is_default"] != true || rule["check_at"] != "09:30" {
		t.Fatalf("rule = %v", rule)
	}
	f.SetNow(at("2026-11-27", 10, 0))
	set := f.Call(t, f.Owner, "set_reminder_rule_mode", map[string]any{"rule_key": "closing_yesterday_brief", "mode": "off"}, 200)
	if set.Body["mode"] != "off" || set.Body["is_default"] != false {
		t.Fatalf("set = %v", set.Body)
	}
	r = f.Call(t, f.Owner, "list_reminder_rules", map[string]any{}, 200)
	if r.Body["rules"].([]any)[0].(map[string]any)["mode"] != "off" {
		t.Fatalf("list = %v", r.Body)
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var before, after []byte
	if err := tx.QueryRow(context.Background(), "SELECT before,after FROM audit_log WHERE action='set_reminder_rule_mode' AND target_kind='ai_reminder_rule' ORDER BY at DESC,id DESC LIMIT 1").Scan(&before, &after); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	var beforeMode, afterMode map[string]any
	if err := json.Unmarshal(before, &beforeMode); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &afterMode); err != nil {
		t.Fatal(err)
	}
	if beforeMode["mode"] != "observe" || afterMode["mode"] != "off" {
		t.Fatalf("audit = %s %s", before, after)
	}
	f.Call(t, f.Owner, "set_reminder_rule_mode", map[string]any{"rule_key": "closing_yesterday_brief", "mode": "off"}, 200)
	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='set_reminder_rule_mode'").Scan(&count); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if count != 1 {
		t.Fatalf("audit rows = %d", count)
	}
}

func TestReminderOpsUnknownRuleAndPermissions(t *testing.T) {
	f := open(t)
	f.SetNow(at("2026-11-27", 10, 0))
	r := f.HTTP(t, ownerCallerOf(f), "set_reminder_rule_mode", map[string]any{"rule_key": "nope", "mode": "off"})
	if r.Status != http.StatusConflict || r.Detail() != "没有这条提醒规则" {
		t.Fatalf("set unknown = %d %q", r.Status, r.Detail())
	}
	r = f.HTTP(t, ownerCallerOf(f), "get_reminder_summary", map[string]any{"rule_key": "nope"})
	if r.Status != http.StatusConflict || r.Detail() != "没有这条提醒规则" {
		t.Fatalf("summary unknown = %d %q", r.Status, r.Detail())
	}
	_, stylist := f.Hire("发型师", "002", "stylist", f.Role("发型师"))
	caller := f.MustCaller(stylist)
	type opCall struct {
		name string
		args map[string]any
	}
	calls := []opCall{
		{"list_reminder_rules", map[string]any{}},
		{"set_reminder_rule_mode", map[string]any{"rule_key": "closing_yesterday_brief", "mode": "off"}},
		{"get_reminder_summary", map[string]any{"rule_key": "closing_yesterday_brief"}},
		{"set_reminder_feedback", map[string]any{"reminder_id": uuid.New(), "feedback": "helpful"}},
	}
	for _, call := range calls {
		r := f.HTTP(t, &caller, call.name, call.args)
		if r.Status != http.StatusForbidden || !strings.Contains(r.Detail(), "permission denied") {
			t.Fatalf("%s = %d %q", call.name, r.Status, r.Detail())
		}
	}
	owner := f.MustCaller(f.Owner)
	for _, call := range calls {
		r := f.AI(t, owner, call.name, call.args)
		if r.Status != http.StatusNotFound || !errors.Is(r.Err, ops.ErrUnknownOperation) {
			t.Fatalf("AI %s = %d %v", call.name, r.Status, r.Err)
		}
	}
}

func TestReminderReadChannels(t *testing.T) {
	f := open(t)
	f.SetNow(at("2026-11-27", 10, 0))
	caller := f.MustCaller(f.Owner)
	calls := []struct {
		name string
		args map[string]any
	}{
		{"list_reminder_rules", map[string]any{}},
		{"get_reminder_summary", map[string]any{"rule_key": "closing_yesterday_brief"}},
	}
	for _, ch := range f.Channels() {
		for _, call := range calls {
			r := ch.Call(t, caller, call.name, call.args)
			if ch.Name == "http" && r.Status != http.StatusOK {
				t.Fatalf("%s %s = %d %v", ch.Name, call.name, r.Status, r.Body)
			}
			if ch.Name == "ai" && (r.Status != http.StatusNotFound || !errors.Is(r.Err, ops.ErrUnknownOperation)) {
				t.Fatalf("%s %s = %d %v", ch.Name, call.name, r.Status, r.Err)
			}
		}
	}
}

func TestReminderSummaryAndFeedback(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	now := at("2026-11-27", 10, 0)
	f.SetNow(now)
	runDefault(t, f.Shop, now, nil)
	emitted := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(emitted) != 1 {
		t.Fatalf("emitted = %d", len(emitted))
	}
	insertReminderRow(t, f, "closing_yesterday_brief", "deduped", "2026-11-27", "extra-1", nil)
	insertReminderRow(t, f, "closing_yesterday_brief", "daily_cap", "2026-11-27", "extra-2", nil)
	insertRunRow(t, f, "closing_yesterday_brief", "2026-11-26", "error", 1)
	r := f.Call(t, f.Owner, "get_reminder_summary", map[string]any{"rule_key": "closing_yesterday_brief", "days": 7}, 200)
	if r.Body["ran_days"].(float64) != 2 || r.Body["matched_days"].(float64) != 1 || r.Body["error_days"].(float64) != 1 {
		t.Fatalf("summary = %v", r.Body)
	}
	if r.Body["last_error_date"] != "2026-11-26" || r.Body["emitted"].(float64) != 1 {
		t.Fatalf("summary = %v", r.Body)
	}
	blocked := r.Body["blocked"].(map[string]any)
	if blocked["deduped"].(float64) != 1 || blocked["daily_cap"].(float64) != 1 {
		t.Fatalf("blocked = %v", blocked)
	}
	if r.Body["average_per_recipient_day"].(float64) != 1 || len(r.Body["samples"].([]any)) != 1 {
		t.Fatalf("summary = %v", r.Body)
	}
	feedback := f.Call(t, f.Owner, "set_reminder_feedback", map[string]any{"reminder_id": emitted[0].ID, "feedback": "helpful"}, 200)
	if feedback.Body["feedback"] != "helpful" {
		t.Fatalf("feedback = %v", feedback.Body)
	}
	r = f.Call(t, f.Owner, "get_reminder_summary", map[string]any{"rule_key": "closing_yesterday_brief"}, 200)
	if r.Body["feedback"].(map[string]any)["helpful"].(float64) != 1 {
		t.Fatalf("summary feedback = %v", r.Body)
	}
	sample := r.Body["samples"].([]any)[0].(map[string]any)
	if sample["feedback"] != "helpful" || sample["ai_written"] != false {
		t.Fatalf("sample = %v", sample)
	}
	f.Call(t, f.Owner, "set_reminder_feedback", map[string]any{"reminder_id": emitted[0].ID, "feedback": nil}, 200)
	r = f.Call(t, f.Owner, "get_reminder_summary", map[string]any{"rule_key": "closing_yesterday_brief"}, 200)
	if r.Body["feedback"].(map[string]any)["helpful"].(float64) != 0 {
		t.Fatalf("cleared feedback = %v", r.Body)
	}
}

func TestFeedbackRejectsOtherStoreBlockedAndMissing(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	now := at("2026-11-27", 10, 0)
	f.SetNow(now)
	runDefault(t, f.Shop, now, nil)
	blocked := insertReminderRow(t, f, "closing_yesterday_brief", "deduped", "2026-11-27", "blocked", nil)
	for _, id := range []uuid.UUID{blocked, uuid.New()} {
		r := f.HTTP(t, ownerCallerOf(f), "set_reminder_feedback", map[string]any{"reminder_id": id, "feedback": "helpful"})
		if r.Status != http.StatusNotFound || r.Detail() != "not found" {
			t.Fatalf("feedback %s = %d %q", id, r.Status, r.Detail())
		}
	}
	otherStore, otherStaff := secondStore(t, f)
	otherReminder := insertReminderAt(t, f, otherStore, otherStaff, "closing_yesterday_brief", "emitted", "2026-11-27", "other", nil)
	r := f.HTTP(t, ownerCallerOf(f), "set_reminder_feedback", map[string]any{"reminder_id": otherReminder, "feedback": "helpful"})
	if r.Status != http.StatusNotFound || r.Detail() != "not found" {
		t.Fatalf("other store feedback = %d %q", r.Status, r.Detail())
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var audit int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='set_reminder_feedback'").Scan(&audit); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if audit != 0 {
		t.Fatalf("feedback wrote audit rows: %d", audit)
	}
}

func TestReminderStoreIsolation(t *testing.T) {
	f := open(t)
	otherStore, _ := secondStore(t, f)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO ai_reminder_rules (tenant_id,store_id,rule_key,mode,updated_by) VALUES ($1,$2,'closing_yesterday_brief','off',$3)`, f.Res.TenantID, otherStore, f.Res.OwnerID)
	dbtest.Commit(t, tx)
	r := f.Call(t, f.Owner, "list_reminder_rules", map[string]any{}, 200)
	if r.Body["rules"].([]any)[0].(map[string]any)["mode"] != "observe" {
		t.Fatalf("other store changed mode: %v", r.Body)
	}
}

func TestReminderSummaryQueryCount(t *testing.T) {
	f := open(t)
	for i := 0; i < 10; i++ {
		insertReminderRow(t, f, "closing_yesterday_brief", "emitted", "2026-11-27", "subject-"+string(rune('a'+i)), nil)
	}
	counter := &queryCounter{}
	config := f.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	app := optest.New(t, pool)
	app.SetNow(at("2026-11-27", 10, 0))
	caller := f.MustCaller(f.Owner)
	counts := []int64{}
	for _, limit := range []int{1, 10} {
		if _, err := f.Pool.Exec(t.Context(), "DELETE FROM ai_reminders WHERE subject_key <> $1", "subject-a"); err != nil {
			t.Fatal(err)
		}
		for i := 1; i < limit; i++ {
			insertReminderRow(t, f, "closing_yesterday_brief", "emitted", "2026-11-27", "subject-"+string(rune('a'+i)), nil)
		}
		counter.all.Store(0)
		r := app.HTTP(t, &caller, "get_reminder_summary", map[string]any{"rule_key": "closing_yesterday_brief", "days": 7})
		if r.Status != 200 {
			t.Fatalf("summary = %d %v", r.Status, r.Body)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatalf("query count grew with samples: %v", counts)
	}
}

type queryCounter struct{ all atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func insertReminderRow(t *testing.T, f *fixture, ruleKey, outcome, day, subject string, aiText *string) uuid.UUID {
	t.Helper()
	return insertReminderAt(t, f, f.Res.StoreID, f.Res.OwnerID, ruleKey, outcome, day, subject, aiText)
}

func insertReminderAt(t *testing.T, f *fixture, storeID, staffID uuid.UUID, ruleKey, outcome, day, subject string, aiText *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	err := tx.QueryRow(context.Background(), `INSERT INTO ai_reminders (tenant_id,store_id,rule_key,rule_version,mode,recipient_staff_id,subject_key,business_date,urgent,facts,fallback_text,ai_text,outcome)
		VALUES ($1,$2,$3,1,'observe',$4,$5,$6,false,'{}'::jsonb,'固定文案',$7,$8) RETURNING id`,
		f.Res.TenantID, storeID, ruleKey, staffID, subject, day, aiText, outcome).Scan(&id)
	if err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	return id
}

func insertRunRow(t *testing.T, f *fixture, ruleKey, day, status string, failures int) {
	t.Helper()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO ai_reminder_runs (tenant_id,store_id,rule_key,business_date,status,failures,matched) VALUES ($1,$2,$3,$4,$5,$6,0) ON CONFLICT (tenant_id,store_id,rule_key,business_date) DO UPDATE SET status=EXCLUDED.status,failures=EXCLUDED.failures`,
		f.Res.TenantID, f.Res.StoreID, ruleKey, day, status, failures)
	dbtest.Commit(t, tx)
}

func secondStore(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
	t.Helper()
	storeID, staffID := uuid.New(), uuid.New()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name,created_at) VALUES($1,$2,'第二家',$3)", storeID, f.Res.TenantID, at("2026-11-01", 12, 0))
	dbtest.Exec(t, tx, "INSERT INTO staff(id,tenant_id,store_id,name,number) VALUES($1,$2,$3,'第二家老板','001')", staffID, f.Res.TenantID, storeID)
	dbtest.Commit(t, tx)
	return storeID, staffID
}

func TestPartialUniqueIndexAllowsObserveAndOn(t *testing.T) {
	f := open(t)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	for _, mode := range []string{"observe", "on"} {
		dbtest.Exec(t, tx, `INSERT INTO ai_reminders (tenant_id,store_id,rule_key,rule_version,mode,recipient_staff_id,subject_key,business_date,urgent,facts,fallback_text,outcome)
			VALUES ($1,$2,'closing_yesterday_brief',1,$3,$4,'2026-11-26','2026-11-27',false,'{}'::jsonb,'固定文案','emitted')`,
			f.Res.TenantID, f.Res.StoreID, mode, f.Res.OwnerID)
	}
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO ai_reminders (tenant_id,store_id,rule_key,rule_version,mode,recipient_staff_id,subject_key,business_date,urgent,facts,fallback_text,outcome)
		VALUES ($1,$2,'closing_yesterday_brief',1,'observe',$3,'2026-11-26','2026-11-27',false,'{}'::jsonb,'固定文案','emitted')`,
		f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID)
	dbtest.Rollback(t, tx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "ai_reminders_emitted_unique" {
		t.Fatalf("duplicate emitted = %v", err)
	}
}
