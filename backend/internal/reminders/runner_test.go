package reminders_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/reminders"
)

func matchRule(key string, configure func(*reminders.Rule), matches func(storeID uuid.UUID, businessDate time.Time) []reminders.Match) reminders.Rule {
	rule := testRule(key, func(_ context.Context, _ *dbgen.Queries, storeID uuid.UUID, businessDate, _ time.Time) ([]reminders.Match, error) {
		return matches(storeID, businessDate), nil
	})
	configure(&rule)
	return rule
}

func oneMatch(s *optest.Shop, subject string) func(uuid.UUID, time.Time) []reminders.Match {
	return func(_ uuid.UUID, _ time.Time) []reminders.Match {
		return []reminders.Match{ownerMatch(s, subject, map[string]any{"date": subject})}
	}
}

func TestReminderGates(t *testing.T) {
	t.Run("deduped", func(t *testing.T) {
		f := open(t)
		rule := matchRule("dedup_rule", func(*reminders.Rule) {}, func(_ uuid.UUID, _ time.Time) []reminders.Match {
			return []reminders.Match{
				ownerMatch(f.Shop, "same", map[string]any{"n": 1}),
				ownerMatch(f.Shop, "same", map[string]any{"n": 1}),
			}
		})
		runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
		rows := remindersByRule(t, f.Shop, "dedup_rule")
		if len(rows) != 2 || rows[0].Outcome != "emitted" || rows[1].Outcome != "deduped" {
			t.Fatalf("rows = %+v", rows)
		}
	})

	t.Run("cooldown", func(t *testing.T) {
		f := open(t)
		rule := matchRule("cooldown_rule", func(rule *reminders.Rule) { rule.Cooldown = 2 }, oneMatch(f.Shop, "same"))
		runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
		runRules(t, f.Shop, at("2026-11-28", 10, 0), nil, []reminders.Rule{rule})
		rows := remindersByRule(t, f.Shop, "cooldown_rule")
		if len(rows) != 2 || rows[0].Outcome != "emitted" || rows[1].Outcome != "cooldown" {
			t.Fatalf("rows = %+v", rows)
		}
	})

	t.Run("max times", func(t *testing.T) {
		f := open(t)
		rule := matchRule("max_times_rule", func(rule *reminders.Rule) { rule.MaxTimes = 2 }, oneMatch(f.Shop, "same"))
		for _, day := range []string{"2026-11-27", "2026-11-28", "2026-11-29"} {
			runRules(t, f.Shop, at(day, 10, 0), nil, []reminders.Rule{rule})
		}
		rows := remindersByRule(t, f.Shop, "max_times_rule")
		if len(rows) != 3 || rows[2].Outcome != "max_times" {
			t.Fatalf("rows = %+v", rows)
		}
	})

	t.Run("expired", func(t *testing.T) {
		f := open(t)
		rule := matchRule("expired_rule", func(rule *reminders.Rule) { rule.MaxAge = 1 }, func(_ uuid.UUID, businessDate time.Time) []reminders.Match {
			subjectDate := businessDate.AddDate(0, 0, -2)
			match := ownerMatch(f.Shop, subjectDate.Format("2006-01-02"), map[string]any{"date": subjectDate.Format("2006-01-02")})
			match.SubjectDate = subjectDate
			return []reminders.Match{match}
		})
		runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
		rows := remindersByRule(t, f.Shop, "expired_rule")
		if len(rows) != 1 || rows[0].Outcome != "expired" {
			t.Fatalf("rows = %+v", rows)
		}
	})

	t.Run("daily cap", func(t *testing.T) {
		f := open(t)
		setDailyCap(t, f, "2026-11-27", 1)
		rule := matchRule("daily_cap_rule", func(*reminders.Rule) {}, func(_ uuid.UUID, _ time.Time) []reminders.Match {
			return []reminders.Match{
				ownerMatch(f.Shop, "a", map[string]any{"n": 1}),
				ownerMatch(f.Shop, "b", map[string]any{"n": 2}),
			}
		})
		runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
		outcomes := outcomeBySubject(t, f.Shop, "daily_cap_rule")
		if outcomes["a"] != "emitted" || outcomes["b"] != "daily_cap" {
			t.Fatalf("outcomes = %v", outcomes)
		}
	})

	t.Run("urgent does not use cap", func(t *testing.T) {
		f := open(t)
		setDailyCap(t, f, "2026-11-27", 1)
		rule := matchRule("urgent_rule", func(*reminders.Rule) {}, func(_ uuid.UUID, _ time.Time) []reminders.Match {
			urgent := ownerMatch(f.Shop, "urgent", map[string]any{"n": 1})
			urgent.Urgent = true
			return []reminders.Match{urgent, ownerMatch(f.Shop, "normal1", map[string]any{"n": 2}), ownerMatch(f.Shop, "normal2", map[string]any{"n": 3})}
		})
		runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
		outcomes := outcomeBySubject(t, f.Shop, "urgent_rule")
		if outcomes["urgent"] != "emitted" || outcomes["normal1"] != "emitted" || outcomes["normal2"] != "daily_cap" {
			t.Fatalf("outcomes = %v", outcomes)
		}
	})

	t.Run("urgent bypasses full cap", func(t *testing.T) {
		f := open(t)
		setDailyCap(t, f, "2026-11-27", 1)
		insertReminderRow(t, f, "filler_rule", "emitted", "2026-11-27", "filler", nil)
		rule := matchRule("urgent_full_rule", func(*reminders.Rule) {}, func(_ uuid.UUID, _ time.Time) []reminders.Match {
			match := ownerMatch(f.Shop, "urgent", map[string]any{"n": 1})
			match.Urgent = true
			return []reminders.Match{match}
		})
		runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
		if got := outcomeBySubject(t, f.Shop, "urgent_full_rule")["urgent"]; got != "emitted" {
			t.Fatalf("urgent outcome = %q", got)
		}
	})
}

func setDailyCap(t *testing.T, f *fixture, day string, cap int) {
	t.Helper()
	f.SetNow(at("2026-11-26", 12, 0))
	f.Call(t, f.Owner, "change_setting", map[string]any{"key": "ai_reminder_daily_cap", "value": cap, "effective_from": day}, 200)
}

func outcomeBySubject(t *testing.T, s *optest.Shop, ruleKey string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, row := range remindersByRule(t, s, ruleKey) {
		out[row.SubjectKey] = row.Outcome
	}
	return out
}

func TestObserveAndOnAreSeparate(t *testing.T) {
	f := open(t)
	f.businessDay("2026-11-26")
	runDefault(t, f.Shop, at("2026-11-27", 10, 0), nil)
	f.businessDay("2026-11-27")
	f.SetNow(at("2026-11-27", 13, 0))
	f.Call(t, f.Owner, "set_reminder_rule_mode", map[string]any{"rule_key": "closing_yesterday_brief", "mode": "on"}, 200)
	runDefault(t, f.Shop, at("2026-11-28", 10, 0), nil)
	rows := remindersByRule(t, f.Shop, "closing_yesterday_brief")
	if len(rows) != 2 || rows[0].Mode != "observe" || rows[1].Mode != "on" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Outcome != "emitted" || rows[1].Outcome != "emitted" {
		t.Fatalf("outcomes = %+v", rows)
	}
}

func TestOffSkipsRule(t *testing.T) {
	f := open(t)
	f.SetNow(at("2026-11-27", 10, 0))
	f.Call(t, f.Owner, "set_reminder_rule_mode", map[string]any{"rule_key": "closing_yesterday_brief", "mode": "off"}, 200)
	runDefault(t, f.Shop, at("2026-11-27", 10, 0), nil)
	if run := runRow(t, f.Shop, "closing_yesterday_brief", "2026-11-27"); run != nil {
		t.Fatalf("off rule ran: %+v", run)
	}
}

func TestRuleFailureRetriesThenStops(t *testing.T) {
	f := open(t)
	var calls atomic.Int32
	rule := testRule("failing_rule", func(context.Context, *dbgen.Queries, uuid.UUID, time.Time, time.Time) ([]reminders.Match, error) {
		calls.Add(1)
		return nil, errors.New("规则炸了")
	})
	now := at("2026-11-27", 10, 0)
	for range 4 {
		runRules(t, f.Shop, now, nil, []reminders.Rule{rule})
	}
	run := runRow(t, f.Shop, "failing_rule", "2026-11-27")
	if run == nil || run.Status != "error" || run.Failures != 3 || run.LastError == nil || *run.LastError != "规则炸了" {
		t.Fatalf("run = %+v", run)
	}
	if calls.Load() != 3 {
		t.Fatalf("evaluate calls = %d", calls.Load())
	}
}

func TestStoreFailureDoesNotStopOtherStore(t *testing.T) {
	f := open(t)
	otherStore := uuid.New()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name,created_at) VALUES($1,$2,'第二家',$3)", otherStore, f.Res.TenantID, at("2026-11-01", 12, 0))
	dbtest.Commit(t, tx)
	rule := testRule("store_rule", func(_ context.Context, _ *dbgen.Queries, storeID uuid.UUID, _, _ time.Time) ([]reminders.Match, error) {
		if storeID == f.Res.StoreID {
			return nil, errors.New("第一家长辈")
		}
		return nil, nil
	})
	runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
	first := runRow(t, f.Shop, "store_rule", "2026-11-27")
	if first == nil || first.Status != "error" || first.Failures != 1 {
		t.Fatalf("first run = %+v", first)
	}
	second := runAtStore(t, f, otherStore, "store_rule", "2026-11-27")
	if second == nil || second.Status != "no_match" {
		t.Fatalf("second run = %+v", second)
	}
}

func runAtStore(t *testing.T, f *fixture, storeID uuid.UUID, ruleKey, day string) *dbgen.AiReminderRun {
	t.Helper()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var row dbgen.AiReminderRun
	err := tx.QueryRow(context.Background(), `SELECT tenant_id,store_id,id,rule_key,business_date,status,failures,last_error,matched,updated_at FROM ai_reminder_runs WHERE store_id=$1 AND rule_key=$2 AND business_date=$3`, storeID, ruleKey, day).
		Scan(&row.TenantID, &row.StoreID, &row.ID, &row.RuleKey, &row.BusinessDate, &row.Status, &row.Failures, &row.LastError, &row.Matched, &row.UpdatedAt)
	dbtest.Rollback(t, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return &row
}

func TestAdvisoryLockSkipsStore(t *testing.T) {
	f := open(t)
	tx, err := f.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dbtest.Rollback(t, tx)
	dbtest.Exec(t, tx, "SELECT pg_advisory_xact_lock(hashtextextended($1::text || '/ai_reminders',0))", f.Res.StoreID.String())
	rule := matchRule("locked_rule", func(*reminders.Rule) {}, oneMatch(f.Shop, "same"))
	runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{rule})
	if run := runRow(t, f.Shop, "locked_rule", "2026-11-27"); run != nil {
		t.Fatalf("ran while lock held: %+v", run)
	}
}

func TestRunLoopStopsOnContext(t *testing.T) {
	f := open(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		reminders.RunLoop(ctx, f.Pool, time.Now, modelFunc(nil), discardLogger())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunLoop did not return after context cancellation")
	}
}
