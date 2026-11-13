package dataio

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/closingguard"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// undo 撤销一批已提交的导入；有外部引用时返回 409 并列出引用。
func undo(ctx context.Context, env ops.Env, in *UndoImportInput) (*ImportBatchDetail, error) {
	batch, err := env.Q.LockImportBatch(ctx, dbgen.LockImportBatchParams{StoreID: env.Caller.StoreID, ID: in.BatchID})
	if err != nil {
		return nil, rowError(err)
	}
	if batch.Status != "committed" {
		return nil, ops.Rule("只能撤销已提交的导入批次")
	}
	// 与提交共用同一把门店咨询锁，撤销和提交不会交错写同一批数据。
	if err := env.Q.LockImportStore(ctx, env.Caller.StoreID.String()); err != nil {
		return nil, err
	}
	dates, err := undoBusinessDates(ctx, env, batch.ID)
	if err != nil {
		return nil, err
	}
	for _, day := range dates {
		if err := closingguard.Guard(ctx, env.Q, env.Caller.StoreID, day); err != nil {
			return nil, err
		}
	}
	before, err := loadDetail(ctx, env, env.Q, env.Caller.StoreID, batch.ID)
	if err != nil {
		return nil, err
	}
	raw, err := env.Q.UndoImportBatch(ctx, dbgen.UndoImportBatchParams{BatchID: batch.ID, StoreID: env.Caller.StoreID})
	if err != nil {
		return nil, err
	}
	var refs []string
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		return nil, ops.Rule("不能撤销这次导入：" + strings.Join(refs, "；"))
	}
	if err := env.Q.MarkImportBatchUndone(ctx, dbgen.MarkImportBatchUndoneParams{ID: batch.ID, UndoneBy: &env.Caller.StaffID, UndoneAt: pgtype.Timestamptz{Time: env.Now, Valid: true}}); err != nil {
		return nil, err
	}
	after, err := loadDetail(ctx, env, env.Q, env.Caller.StoreID, batch.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "undo_import", audit.ImportBatchTarget(batch.ID), before.ImportBatch, after.ImportBatch); err != nil {
		return nil, err
	}
	return after, nil
}

func undoBusinessDates(ctx context.Context, env ops.Env, batch uuid.UUID) ([]time.Time, error) {
	q := env.Q
	dates := []time.Time{}
	ledgers, err := q.UndoCardLedgerDates(ctx, dbgen.UndoCardLedgerDatesParams{TenantID: env.Caller.TenantID, BatchID: batch})
	if err != nil {
		return nil, err
	}
	for _, d := range ledgers {
		dates = append(dates, d.Time)
	}
	counts, err := q.UndoCardCountDates(ctx, dbgen.UndoCardCountDatesParams{TenantID: env.Caller.TenantID, BatchID: batch})
	if err != nil {
		return nil, err
	}
	for _, d := range counts {
		dates = append(dates, d.Time)
	}
	points, err := q.UndoCardPointsDates(ctx, dbgen.UndoCardPointsDatesParams{TenantID: env.Caller.TenantID, BatchID: batch})
	if err != nil {
		return nil, err
	}
	for _, d := range points {
		dates = append(dates, d.Time)
	}
	times := []time.Time{}
	cards, err := q.UndoCreatedCardTimes(ctx, dbgen.UndoCreatedCardTimesParams{TenantID: env.Caller.TenantID, BatchID: batch})
	if err != nil {
		return nil, err
	}
	for _, t := range cards {
		times = append(times, t.Time)
	}
	customers, err := q.UndoCreatedCustomerTimes(ctx, dbgen.UndoCreatedCustomerTimesParams{TenantID: env.Caller.TenantID, BatchID: batch})
	if err != nil {
		return nil, err
	}
	for _, t := range customers {
		times = append(times, t.Time)
	}
	converted, err := settings.BusinessDates(ctx, q, env.Caller.StoreID, times)
	if err != nil {
		return nil, err
	}
	dates = append(dates, converted...)
	seen := map[string]bool{}
	out := []time.Time{}
	for _, d := range dates {
		key := d.Format(settings.DateLayout)
		if !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	return out, nil
}
