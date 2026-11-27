package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

const (
	// RunInterval 是后台提醒循环的间隔。
	RunInterval = 5 * time.Minute
	// StoreTimeout 是单家门店一轮提醒检查的时限。
	StoreTimeout = 5 * time.Minute
)

type pendingReminder struct {
	id    uuid.UUID
	rule  Rule
	facts json.RawMessage
}

// RunLoop 立即执行一轮提醒检查，之后每 RunInterval 一轮，直到 ctx 结束。
func RunLoop(ctx context.Context, pool *pgxpool.Pool, now func() time.Time, model func() ai.Model, logger *slog.Logger) {
	RunOnce(ctx, pool, now(), model, logger)
	ticker := time.NewTicker(RunInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunOnce(ctx, pool, now(), model, logger)
		}
	}
}

// RunOnce 用生产注册表对全部门店执行一轮提醒检查；一家门店出错只记日志，不影响其他门店。
func RunOnce(ctx context.Context, pool *pgxpool.Pool, now time.Time, model func() ai.Model, logger *slog.Logger) {
	runOnce(ctx, pool, now, model, logger, registeredRules)
}

// RunOnceWithRules 用给定规则执行一轮提醒检查，供测试注入假规则；不改变生产注册表。
func RunOnceWithRules(ctx context.Context, pool *pgxpool.Pool, now time.Time, model func() ai.Model, logger *slog.Logger, rules []Rule) {
	runOnce(ctx, pool, now, model, logger, rules)
}

func runOnce(ctx context.Context, pool *pgxpool.Pool, now time.Time, model func() ai.Model, logger *slog.Logger, rules []Rule) {
	if logger == nil {
		logger = slog.Default()
	}
	// 选店只需要租户和门店标识，在 InTenant 之外用服务连接的数据库属主角色读取。
	stores, err := dbgen.New(pool).ListStoreTenants(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Error("ai reminders: list stores", "err", err)
		}
		return
	}
	for _, store := range stores {
		if ctx.Err() != nil {
			return
		}
		if err := runStoreSafely(ctx, pool, store.TenantID, store.ID, now, model, logger, rules); err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Error("ai reminders", "tenant_id", store.TenantID, "store_id", store.ID, "err", err)
			}
			continue
		}
	}
}

// runStoreSafely 给一家门店加超时和 panic 恢复，失败只影响这一家。
func runStoreSafely(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, now time.Time, model func() ai.Model, logger *slog.Logger, rules []Rule) (err error) {
	ctx, cancel := context.WithTimeout(ctx, StoreTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return runStore(ctx, pool, tenantID, storeID, now, model, logger, rules)
}

func runStore(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, now time.Time, model func() ai.Model, logger *slog.Logger, rules []Rule) error {
	var pending []pendingReminder
	err := db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		locked, err := q.LockReminderStore(ctx, storeID)
		if err != nil {
			return err
		}
		if !locked {
			return nil
		}
		businessDate, ruleset, err := settings.CurrentRules(ctx, q, storeID, now)
		if err != nil {
			return err
		}
		modes, err := effectiveModes(ctx, q, storeID)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			mode := modes[rule.Key]
			if mode == "" {
				mode = rule.defaultMode()
			}
			if mode == ModeOff {
				continue
			}
			if now.In(settings.Location).Format("15:04") < rule.CheckAt {
				continue
			}
			run, hasRun, err := reminderRun(ctx, q, storeID, rule.Key, businessDate)
			if err != nil {
				return err
			}
			if hasRun && (run.Status != runError || run.Failures >= maxRuleFailures) {
				continue
			}
			if err := q.SavepointReminderRule(ctx); err != nil {
				return err
			}
			created, ruleErr := runRule(ctx, q, tenantID, storeID, rule, mode, businessDate, now, ruleset.AIReminderDailyCap())
			if ruleErr != nil {
				if err := q.RollbackReminderRule(ctx); err != nil {
					return err
				}
				failures := int32(1)
				if hasRun {
					failures = run.Failures + 1
				}
				lastError := shortError(ruleErr.Error())
				if _, err := q.UpsertReminderRun(ctx, dbgen.UpsertReminderRunParams{
					TenantID: tenantID, StoreID: storeID, RuleKey: rule.Key, BusinessDate: pgDate(businessDate),
					Status: runError, Failures: failures, LastError: &lastError, Matched: 0,
				}); err != nil {
					return err
				}
				if err := q.ReleaseReminderRule(ctx); err != nil {
					return err
				}
				logger.Error("ai reminders: rule failed", "tenant_id", tenantID, "store_id", storeID, "rule_key", rule.Key, "err", ruleErr)
				continue
			}
			if err := q.ReleaseReminderRule(ctx); err != nil {
				return err
			}
			pending = append(pending, created...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeTexts(ctx, pool, tenantID, storeID, pending, model, logger)
}

func effectiveModes(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID) (map[string]Mode, error) {
	rows, err := q.ListReminderRuleModes(ctx, storeID)
	if err != nil {
		return nil, err
	}
	out := map[string]Mode{}
	for _, row := range rows {
		out[row.RuleKey] = Mode(row.Mode)
	}
	return out, nil
}

func reminderRun(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ruleKey string, businessDate time.Time) (dbgen.AiReminderRun, bool, error) {
	run, err := q.GetReminderRun(ctx, dbgen.GetReminderRunParams{StoreID: storeID, RuleKey: ruleKey, BusinessDate: pgDate(businessDate)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiReminderRun{}, false, nil
	}
	if err != nil {
		return dbgen.AiReminderRun{}, false, err
	}
	return run, true, nil
}

func runRule(ctx context.Context, q *dbgen.Queries, tenantID, storeID uuid.UUID, rule Rule, mode Mode, businessDate, now time.Time, dailyCap int) ([]pendingReminder, error) {
	matches, err := rule.Evaluate(ctx, q, storeID, businessDate, now)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		if _, err := q.UpsertReminderRun(ctx, dbgen.UpsertReminderRunParams{
			TenantID: tenantID, StoreID: storeID, RuleKey: rule.Key, BusinessDate: pgDate(businessDate),
			Status: runNoMatch, Failures: 0, Matched: 0,
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	var pending []pendingReminder
	for _, match := range matches {
		outcome, err := gate(ctx, q, storeID, rule, match, mode, businessDate, dailyCap)
		if err != nil {
			return nil, err
		}
		facts, err := json.Marshal(match.Facts)
		if err != nil {
			return nil, err
		}
		row, err := q.AddReminder(ctx, dbgen.AddReminderParams{
			TenantID: tenantID, StoreID: storeID, RuleKey: rule.Key, RuleVersion: int32(rule.Version),
			Mode: string(mode), RecipientStaffID: match.RecipientStaffID, SubjectKey: match.SubjectKey,
			BusinessDate: pgDate(businessDate), Urgent: match.Urgent, Facts: facts,
			FallbackText: match.FallbackText, Outcome: outcome,
		})
		if err != nil {
			return nil, err
		}
		if outcome == outcomeEmitted {
			pending = append(pending, pendingReminder{id: row.ID, rule: rule, facts: facts})
		}
	}
	if _, err := q.UpsertReminderRun(ctx, dbgen.UpsertReminderRunParams{
		TenantID: tenantID, StoreID: storeID, RuleKey: rule.Key, BusinessDate: pgDate(businessDate),
		Status: runDone, Failures: 0, Matched: int32(len(matches)),
	}); err != nil {
		return nil, err
	}
	return pending, nil
}

func gate(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, rule Rule, match Match, mode Mode, businessDate time.Time, dailyCap int) (string, error) {
	deduped, err := q.ReminderEmittedSameDay(ctx, dbgen.ReminderEmittedSameDayParams{
		StoreID: storeID, RuleKey: rule.Key, Mode: string(mode), RecipientStaffID: match.RecipientStaffID,
		SubjectKey: match.SubjectKey, BusinessDate: pgDate(businessDate),
	})
	if err != nil {
		return "", err
	}
	if deduped {
		return outcomeDeduped, nil
	}
	if rule.Cooldown > 0 {
		inCooldown, err := q.ReminderEmittedInCooldown(ctx, dbgen.ReminderEmittedInCooldownParams{
			StoreID: storeID, RuleKey: rule.Key, RecipientStaffID: match.RecipientStaffID,
			SubjectKey: match.SubjectKey, FromDate: pgDate(businessDate.AddDate(0, 0, -rule.Cooldown)), BusinessDate: pgDate(businessDate),
		})
		if err != nil {
			return "", err
		}
		if inCooldown {
			return outcomeCooldown, nil
		}
	}
	if rule.MaxTimes > 0 {
		count, err := q.ReminderEmittedCount(ctx, dbgen.ReminderEmittedCountParams{
			StoreID: storeID, RuleKey: rule.Key, RecipientStaffID: match.RecipientStaffID, SubjectKey: match.SubjectKey,
		})
		if err != nil {
			return "", err
		}
		if count >= int64(rule.MaxTimes) {
			return outcomeMaxTimes, nil
		}
	}
	if rule.MaxAge >= 0 && !match.SubjectDate.IsZero() && match.SubjectDate.Before(businessDate.AddDate(0, 0, -rule.MaxAge)) {
		return outcomeExpired, nil
	}
	if !match.Urgent {
		count, err := q.ReminderDailyCount(ctx, dbgen.ReminderDailyCountParams{
			StoreID: storeID, RecipientStaffID: match.RecipientStaffID, Mode: string(mode), BusinessDate: pgDate(businessDate),
		})
		if err != nil {
			return "", err
		}
		if count >= int64(dailyCap) {
			return outcomeDailyCap, nil
		}
	}
	return outcomeEmitted, nil
}

func writeTexts(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, pending []pendingReminder, model func() ai.Model, logger *slog.Logger) error {
	if len(pending) == 0 || model == nil {
		return nil
	}
	selected := model()
	if selected == nil {
		return nil
	}
	type update struct {
		id   uuid.UUID
		text string
	}
	updates := []update{}
	for _, reminder := range pending {
		if ctx.Err() != nil {
			return nil
		}
		text, ok := GenerateText(ctx, selected, reminder.rule, reminder.facts)
		if !ok {
			continue
		}
		updates = append(updates, update{id: reminder.id, text: text})
	}
	if len(updates) == 0 {
		return nil
	}
	return db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		for _, item := range updates {
			rows, err := q.SetReminderAIText(ctx, dbgen.SetReminderAITextParams{AiText: &item.text, ID: item.id, StoreID: storeID})
			if err != nil {
				return err
			}
			if rows != 1 {
				logger.Error("ai reminders: reminder disappeared before writing text", "reminder_id", item.id)
			}
		}
		return nil
	})
}

func shortError(text string) string {
	const limit = 500
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func pgDate(day time.Time) pgtype.Date { return pgtype.Date{Time: day, Valid: true} }
