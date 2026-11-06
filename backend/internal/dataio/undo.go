package dataio

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
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
