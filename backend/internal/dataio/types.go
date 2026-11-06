// Package dataio 从约定的中间 Excel 导入历史顾客与卡，并导出本店全量数据。
package dataio

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

const (
	// MaxUploadBytes 是导入文件的大小上限。
	MaxUploadBytes = 8 << 20
	// MaxUnzipBytes 是 xlsx 解压后的总大小上限，防止压缩炸弹把内存或临时目录撑满。
	MaxUnzipBytes = 64 << 20
	// MaxUnzipXMLBytes 是单个工作表或共享字符串 XML 的内存上限，超过时 excelize 会改写到临时文件。
	MaxUnzipXMLBytes = 16 << 20
	// MaxRowsPerSheet 是每张工作表的数据行上限（不含表头）。
	MaxRowsPerSheet = 20000
	// MaxSummaryRows 是汇总工作表的数据行上限；汇总只允许一行，仍留出足够的错误位置。
	MaxSummaryRows = 1000

	sheetCustomers   = "顾客"
	sheetStoredCards = "储值卡"
	sheetCountCards  = "次卡套餐"
	sheetSummary     = "汇总"
)

// ImportTotals 是一批导入的条数与金额合计。
type ImportTotals struct {
	Customers         int32  `json:"customers"`
	StoredValueCards  int32  `json:"stored_value_cards"`
	Principal         string `json:"principal"`
	Bonus             string `json:"bonus"`
	CountPackageCards int32  `json:"count_package_cards"`
}

// ImportDifferences 是汇总表、文件行与实际写入之间的差额。
type ImportDifferences struct {
	DeclaredMinusFile    ImportTotals `json:"declared_minus_file"`
	DeclaredMinusWritten ImportTotals `json:"declared_minus_written"`
}

// ImportBatch 是一批导入的摘要。
type ImportBatch struct {
	ID          uuid.UUID         `json:"id"`
	Status      string            `json:"status" enum:"previewed,committed,undone"`
	FileName    string            `json:"file_name"`
	CreatedAt   time.Time         `json:"created_at"`
	CommittedAt *time.Time        `json:"committed_at" nullable:"true"`
	UndoneAt    *time.Time        `json:"undone_at" nullable:"true"`
	Declared    ImportTotals      `json:"declared"`
	File        ImportTotals      `json:"file"`
	Written     ImportTotals      `json:"written"`
	Differences ImportDifferences `json:"differences"`
	ErrorRows   int32             `json:"error_rows"`
	RowCount    int32             `json:"row_count"`
}

// ImportBatchRow 是一行导入结果。
type ImportBatchRow struct {
	ID           uuid.UUID     `json:"id"`
	Sheet        string        `json:"sheet" enum:"顾客,储值卡,次卡套餐"`
	RowNumber    int32         `json:"row_number"`
	Kind         string        `json:"kind" enum:"customer,stored_value,count_package"`
	SourceKey    string        `json:"source_key"`
	Name         *string       `json:"name" nullable:"true"`
	Phone        *string       `json:"phone" nullable:"true"`
	CardTypeName *string       `json:"card_type_name" nullable:"true"`
	ItemName     *string       `json:"item_name" nullable:"true"`
	Principal    *money.Amount `json:"principal" nullable:"true"`
	Bonus        *money.Amount `json:"bonus" nullable:"true"`
	Points       *int32        `json:"points" nullable:"true"`
	PaidCount    *int32        `json:"paid_count" nullable:"true"`
	GiftCount    *int32        `json:"gift_count" nullable:"true"`
	ExpiresOn    *string       `json:"expires_on" nullable:"true"`
	Error        *string       `json:"error" nullable:"true"`
	Action       *string       `json:"action" nullable:"true" enum:"created,linked"`
	EntityID     *uuid.UUID    `json:"entity_id" nullable:"true"`
}

// ImportBatchDetail 是一批导入的完整结果。
type ImportBatchDetail struct {
	ImportBatch
	Rows []ImportBatchRow `json:"rows"`
}

// CommitImportInput 指定要提交的导入批次。
type CommitImportInput struct {
	BatchID uuid.UUID `json:"batch_id"`
}

// UndoImportInput 指定要撤销的导入批次。
type UndoImportInput struct {
	BatchID uuid.UUID `json:"batch_id"`
}

// GetImportBatchInput 指定要查看的导入批次。
type GetImportBatchInput struct {
	BatchID uuid.UUID `json:"batch_id"`
}

// ListImportBatchesInput 是列出导入批次的空参数。
type ListImportBatchesInput struct{}

// ImportBatchList 是导入批次列表。
type ImportBatchList struct {
	Items []ImportBatch `json:"items"`
}

func zeroTotals() ImportTotals { return ImportTotals{Principal: "0.00", Bonus: "0.00"} }

func addAmount(total *money.Total, amount money.Amount) {
	*total = total.AddProduct(amount, 1)
}

func totalString(total money.Total) string { return total.Format() }

func subtractTotals(a, b ImportTotals) ImportTotals {
	return ImportTotals{
		Customers:         a.Customers - b.Customers,
		StoredValueCards:  a.StoredValueCards - b.StoredValueCards,
		CountPackageCards: a.CountPackageCards - b.CountPackageCards,
		Principal:         formatCents(parseCents(a.Principal) - parseCents(b.Principal)),
		Bonus:             formatCents(parseCents(a.Bonus) - parseCents(b.Bonus)),
	}
}

func parseCents(value string) int64 {
	value = strings.TrimSpace(value)
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = strings.TrimPrefix(value, "-")
	}
	whole, fraction, _ := strings.Cut(value, ".")
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0
	}
	n *= 100
	if len(fraction) > 0 {
		f, err := strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0
		}
		if len(fraction) == 1 {
			f *= 10
		}
		n += f
	}
	if negative {
		return -n
	}
	return n
}

func formatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return sign + strconv.FormatInt(cents/100, 10) + "." + fmt.Sprintf("%02d", cents%100)
}
