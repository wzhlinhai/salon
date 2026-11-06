package dataio

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return ptr(value.Time)
}

func batchFromGet(row dbgen.GetImportBatchRow) ImportBatch {
	declared := ImportTotals{Customers: row.DeclaredCustomers, StoredValueCards: row.DeclaredStoredCards, Principal: row.DeclaredPrincipal, Bonus: row.DeclaredBonus, CountPackageCards: row.DeclaredCountCards}
	file := ImportTotals{Customers: row.FileCustomers, StoredValueCards: row.FileStoredCards, Principal: row.FilePrincipal, Bonus: row.FileBonus, CountPackageCards: row.FileCountCards}
	written := ImportTotals{Customers: row.WrittenCustomers, StoredValueCards: row.WrittenStoredCards, Principal: row.WrittenPrincipal, Bonus: row.WrittenBonus, CountPackageCards: row.WrittenCountCards}
	return ImportBatch{ID: row.ID, Status: row.Status, FileName: row.FileName, CreatedAt: row.CreatedAt.Time, CommittedAt: timePointer(row.CommittedAt), UndoneAt: timePointer(row.UndoneAt), Declared: declared, File: file, Written: written, Differences: ImportDifferences{DeclaredMinusFile: subtractTotals(declared, file), DeclaredMinusWritten: subtractTotals(declared, written)}}
}

func batchFromList(row dbgen.ListImportBatchesRow) ImportBatch {
	declared := ImportTotals{Customers: row.DeclaredCustomers, StoredValueCards: row.DeclaredStoredCards, Principal: row.BDeclaredPrincipal, Bonus: row.BDeclaredBonus, CountPackageCards: row.DeclaredCountCards}
	file := ImportTotals{Customers: row.FileCustomers, StoredValueCards: row.FileStoredCards, Principal: row.BFilePrincipal, Bonus: row.BFileBonus, CountPackageCards: row.FileCountCards}
	written := ImportTotals{Customers: row.WrittenCustomers, StoredValueCards: row.WrittenStoredCards, Principal: row.BWrittenPrincipal, Bonus: row.BWrittenBonus, CountPackageCards: row.WrittenCountCards}
	return ImportBatch{ID: row.ID, Status: row.Status, FileName: row.FileName, CreatedAt: row.CreatedAt.Time, CommittedAt: timePointer(row.CommittedAt), UndoneAt: timePointer(row.UndoneAt), Declared: declared, File: file, Written: written, Differences: ImportDifferences{DeclaredMinusFile: subtractTotals(declared, file), DeclaredMinusWritten: subtractTotals(declared, written)}, ErrorRows: row.ErrorRows, RowCount: row.RowCount}
}

func rowView(env ops.Env, row dbgen.ImportBatchRow) ImportBatchRow {
	phone := row.Phone
	if phone != nil && !env.Caller.Has(perm.CustomerFullPhone) {
		masked := privacy.MaskPhone(*phone)
		phone = &masked
	}
	out := ImportBatchRow{ID: row.ID, Sheet: row.Sheet, RowNumber: row.RowNumber, Kind: row.Kind, SourceKey: row.SourceKey, Name: row.Name, Phone: phone, CardTypeName: row.CardTypeName, ItemName: row.ItemName, Points: row.Points, PaidCount: row.PaidCount, GiftCount: row.GiftCount, ExpiresOn: dateString(row.ExpiresOn), Error: row.Error, Action: row.Action, EntityID: row.EntityID}
	if row.Kind != "customer" {
		out.Principal, out.Bonus = amountPointer(row.Principal), amountPointer(row.Bonus)
	}
	return out
}

func loadDetail(ctx context.Context, env ops.Env, q *dbgen.Queries, storeID, batchID uuid.UUID) (*ImportBatchDetail, error) {
	row, err := q.GetImportBatch(ctx, dbgen.GetImportBatchParams{StoreID: storeID, ID: batchID})
	if err != nil {
		return nil, rowError(err)
	}
	rows, err := q.ListImportBatchRows(ctx, dbgen.ListImportBatchRowsParams{StoreID: storeID, BatchID: batchID})
	if err != nil {
		return nil, err
	}
	out := &ImportBatchDetail{ImportBatch: batchFromGet(row), Rows: make([]ImportBatchRow, 0, len(rows))}
	for _, row := range rows {
		if row.Error != nil {
			out.ErrorRows++
		}
		out.Rows = append(out.Rows, rowView(env, row))
	}
	out.RowCount = int32(len(rows))
	return out, nil
}

func rowError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}

func listBatches(ctx context.Context, env ops.Env, _ *ListImportBatchesInput) (*ImportBatchList, error) {
	rows, err := env.Q.ListImportBatches(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &ImportBatchList{Items: make([]ImportBatch, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, batchFromList(row))
	}
	return out, nil
}

func getBatch(ctx context.Context, env ops.Env, in *GetImportBatchInput) (*ImportBatchDetail, error) {
	return loadDetail(ctx, env, env.Q, env.Caller.StoreID, in.BatchID)
}

func dateString(value pgtype.Date) *string {
	if !value.Valid {
		return nil
	}
	return ptr(value.Time.Format(time.DateOnly))
}

func amountPointer(value money.Amount) *money.Amount { return &value }
