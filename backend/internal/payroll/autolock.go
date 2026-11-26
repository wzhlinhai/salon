package payroll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// AutoLockInterval 是后台自动锁月循环的间隔。
const AutoLockInterval = time.Hour

// AutoLockStoreTimeout 是单家门店一轮自动锁月的时限。
const AutoLockStoreTimeout = 5 * time.Minute

// RunAutoLock 立即执行一轮自动锁月，之后每 AutoLockInterval 一轮，直到 ctx 结束。
func RunAutoLock(ctx context.Context, pool *pgxpool.Pool, now func() time.Time, logger *slog.Logger) {
	AutoLockOnce(ctx, pool, now(), logger)
	ticker := time.NewTicker(AutoLockInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			AutoLockOnce(ctx, pool, now(), logger)
		}
	}
}

// AutoLockOnce 对全部门店执行一轮自动锁月；一家门店出错只记日志，不影响其他门店。
func AutoLockOnce(ctx context.Context, pool *pgxpool.Pool, now time.Time, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	// 选店只需要租户和门店标识，在 InTenant 之外用服务连接的数据库属主角色读取。
	stores, err := dbgen.New(pool).ListStoreTenants(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Error("payroll auto lock: list stores", "err", err)
		}
		return
	}
	for _, store := range stores {
		if ctx.Err() != nil {
			return
		}
		if err := autoLockStoreSafely(ctx, pool, store.TenantID, store.ID, now); err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Error("payroll auto lock", "tenant_id", store.TenantID, "store_id", store.ID, "err", err)
			}
			continue
		}
	}
}

// autoLockStoreSafely 给一家门店加超时和 panic 恢复，失败只影响这一家。
func autoLockStoreSafely(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, now time.Time) (err error) {
	ctx, cancel := context.WithTimeout(ctx, AutoLockStoreTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return AutoLockStore(ctx, pool, tenantID, storeID, now)
}

// autoLockDate 返回结算月 m 的自动锁定日：下个月的第 day 号。
func autoLockDate(m payrollmonth.Month, day int) time.Time {
	return time.Date(m.First().Year(), m.First().Month()+1, day, 0, 0, 0, 0, time.UTC)
}

// AutoLockStore 对一家门店执行一轮自动锁月：按从早到晚的顺序锁定到期月份，遇到第一个失败就停止本轮。
func AutoLockStore(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, now time.Time) error {
	var months []payrollmonth.Month
	err := db.InTenantSnapshot(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		today, rules, err := settings.CurrentRules(ctx, q, storeID, now)
		if err != nil {
			return err
		}
		statuses, err := payrollmonth.List(ctx, q, storeID, now)
		if err != nil {
			return err
		}
		for i := len(statuses) - 1; i >= 0; i-- {
			status := statuses[i]
			if status.Settlement != nil {
				continue
			}
			if autoLockDate(status.Month, rules.PayrollAutoLockDay()).After(today) {
				break
			}
			months = append(months, status.Month)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, month := range months {
		if err := autoLockMonth(ctx, pool, tenantID, storeID, month, now); err != nil {
			return fmt.Errorf("lock %s: %w", month, err)
		}
	}
	return nil
}

// autoLockMonth 在一个独立事务里锁定一个月；已锁定时跳过，与手动锁并发也安全。
func autoLockMonth(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, month payrollmonth.Month, now time.Time) error {
	return db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		if err := q.LockPayroll(ctx, storeID); err != nil {
			return err
		}
		existing, err := payrollmonth.GetSettlement(ctx, q, storeID, month)
		if err != nil {
			return err
		}
		if existing != nil {
			return nil
		}
		_, err = LockMonth(ctx, ops.Env{Now: now, Caller: ops.Caller{TenantID: tenantID, StoreID: storeID}, Q: q}, month, uuid.Nil)
		return err
	})
}
