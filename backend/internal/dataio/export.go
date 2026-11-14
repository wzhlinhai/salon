package dataio

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
)

const (
	exportCustomers = "顾客"
	exportStored    = "储值卡"
	exportCounts    = "次卡套餐"
	exportLedger    = "卡流水"
	exportOrders    = "单据"
	exportPayments  = "支付"
	exportStaffName = "员工"
	exportItems     = "项目"
	exportCardTypes = "卡种"
)

var exportOrder = []string{exportCustomers, exportStored, exportCounts, exportLedger, exportOrders, exportPayments, exportStaffName, exportItems, exportCardTypes}

// exportPageSize 是导出大表时每次查询读取的行数；测试会调小它验证分页上限。
var exportPageSize int32 = 1000

// exportPageObserver 只在测试中记录每个分页查询返回的行数。
var exportPageObserver func(name string, rows int)

func observeExportPage(name string, rows int) {
	if exportPageObserver != nil {
		exportPageObserver(name, rows)
	}
}

// Export 把本店全量数据写成可被 excelize 读回的 xlsx，并记录一次导出日志。
func Export(ctx context.Context, w io.Writer, pool *pgxpool.Pool, caller ops.Caller, now time.Time) error {
	if !caller.Has(perm.DataExport) {
		return ops.ErrForbidden
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", exportCustomers); err != nil {
		return err
	}
	for _, name := range exportOrder[1:] {
		if _, err := f.NewSheet(name); err != nil {
			return err
		}
	}
	err := db.InTenantSnapshot(ctx, pool, caller.TenantID, func(q *dbgen.Queries) error {
		env := ops.Env{Now: now, Caller: caller, Q: q}
		counts := map[string]int{}
		if err := exportCustomersSheet(ctx, f, q, caller, counts); err != nil {
			return err
		}
		if err := exportStoredSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportCountSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportLedgerSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportOrdersSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportPaymentsSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportStaffSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportItemsSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		if err := exportCardTypesSheet(ctx, f, q, caller.StoreID, counts); err != nil {
			return err
		}
		return audit.Record(ctx, env, "export_all", audit.DataExportTarget(uuid.New()), nil, map[string]any{"sheets": counts})
	})
	if err != nil {
		return err
	}
	return f.Write(w)
}

func streamSheet(f *excelize.File, name string, headers []string, write func(*excelize.StreamWriter) error) error {
	sw, err := f.NewStreamWriter(name)
	if err != nil {
		return err
	}
	row := make([]any, len(headers))
	for i := range headers {
		row[i] = headers[i]
	}
	if err := sw.SetRow("A1", row); err != nil {
		return err
	}
	if write != nil {
		if err := write(sw); err != nil {
			return err
		}
	}
	return sw.Flush()
}

func setRow(sw *excelize.StreamWriter, row int, values []string) error {
	items := make([]any, len(values))
	for i := range values {
		items[i] = values[i]
	}
	return sw.SetRow(fmt.Sprintf("A%d", row), items)
}

func exportCustomersSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, caller ops.Caller, counts map[string]int) error {
	return streamSheet(f, exportCustomers, customerHeaders, func(sw *excelize.StreamWriter) error {
		line := 2
		var after *uuid.UUID
		for {
			rows, err := q.ExportCustomersPage(ctx, dbgen.ExportCustomersPageParams{StoreID: caller.StoreID, AfterID: after, RowLimit: exportPageSize})
			if err != nil {
				return err
			}
			observeExportPage("customers", len(rows))
			for _, row := range rows {
				gender := ""
				if row.Gender != nil {
					gender = map[string]string{"male": "男", "female": "女"}[*row.Gender]
				}
				phone := stringValue(row.Phone)
				if phone != "" && !caller.Has(perm.CustomerFullPhone) {
					phone = privacy.MaskPhone(phone)
				}
				if err := setRow(sw, line, []string{row.ID.String(), stringValue(row.Name), phone, gender}); err != nil {
					return err
				}
				line++
			}
			counts[exportCustomers] += len(rows)
			if len(rows) < int(exportPageSize) {
				return nil
			}
			id := rows[len(rows)-1].ID
			after = &id
		}
	})
}

func exportStoredSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	return streamSheet(f, exportStored, storedHeaders, func(sw *excelize.StreamWriter) error {
		line := 2
		var after *uuid.UUID
		for {
			rows, err := q.ExportStoredCardsPage(ctx, dbgen.ExportStoredCardsPageParams{StoreID: storeID, AfterCardID: after, RowLimit: exportPageSize})
			if err != nil {
				return err
			}
			observeExportPage("stored_cards", len(rows))
			for _, row := range rows {
				if err := setRow(sw, line, []string{row.ID.String(), row.CustomerID.String(), row.CardTypeName, row.Principal, row.Bonus, dateValue(row.ExpiresOn), fmt.Sprint(row.Points)}); err != nil {
					return err
				}
				line++
			}
			counts[exportStored] += len(rows)
			if len(rows) < int(exportPageSize) {
				return nil
			}
			id := rows[len(rows)-1].ID
			after = &id
		}
	})
}

func exportCountSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	return streamSheet(f, exportCounts, countHeaders, func(sw *excelize.StreamWriter) error {
		line := 2
		var afterCard, afterItem *uuid.UUID
		for {
			rows, err := q.ExportCountCardsPage(ctx, dbgen.ExportCountCardsPageParams{StoreID: storeID, AfterCardID: afterCard, AfterItemID: afterItem, RowLimit: exportPageSize})
			if err != nil {
				return err
			}
			observeExportPage("count_cards", len(rows))
			for _, row := range rows {
				if err := setRow(sw, line, []string{row.ID.String(), row.CustomerID.String(), row.CardTypeName, row.ItemName, fmt.Sprint(row.PaidCount), fmt.Sprint(row.GiftCount), dateValue(row.ExpiresOn)}); err != nil {
					return err
				}
				line++
			}
			counts[exportCounts] += len(rows)
			if len(rows) < int(exportPageSize) {
				return nil
			}
			cardID := rows[len(rows)-1].ID
			itemID := rows[len(rows)-1].ItemID
			afterCard, afterItem = &cardID, &itemID
		}
	})
}

func exportLedgerSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	headers := []string{"流水ID", "卡ID", "类型", "本金变动前", "本金变动", "本金变动后", "赠送金变动前", "赠送金变动", "赠送金变动后", "营业日", "时间", "操作员工"}
	return streamSheet(f, exportLedger, headers, func(sw *excelize.StreamWriter) error {
		line := 2
		var after *uuid.UUID
		for {
			rows, err := q.ExportCardLedgerPage(ctx, dbgen.ExportCardLedgerPageParams{StoreID: storeID, AfterID: after, RowLimit: exportPageSize})
			if err != nil {
				return err
			}
			observeExportPage("card_ledger", len(rows))
			for _, row := range rows {
				if err := setRow(sw, line, []string{row.ID.String(), row.CardID.String(), row.Kind, row.PrincipalBefore, row.PrincipalDelta, row.PrincipalAfter, row.BonusBefore, row.BonusDelta, row.BonusAfter, dateValue(row.BusinessDate), timeValue(row.At), row.ActorName}); err != nil {
					return err
				}
				line++
			}
			counts[exportLedger] += len(rows)
			if len(rows) < int(exportPageSize) {
				return nil
			}
			id := rows[len(rows)-1].ID
			after = &id
		}
	})
}

func exportOrdersSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	headers := []string{"单ID", "顾客ID", "性别", "指定", "状态", "营业日", "开单时间", "开单员工", "结账ID", "合并到", "补录单", "项目行ID", "项目", "种类", "价格", "折扣", "价格来源", "添加时间", "去掉时间"}
	rows := 0
	err := streamSheet(f, exportOrders, headers, func(sw *excelize.StreamWriter) error {
		line := 2
		var after *uuid.UUID
		for {
			orders, err := q.ExportOrdersPage(ctx, dbgen.ExportOrdersPageParams{StoreID: storeID, AfterID: after, RowLimit: exportPageSize})
			if err != nil {
				return err
			}
			observeExportPage("orders", len(orders))
			ids := make([]uuid.UUID, len(orders))
			for i, order := range orders {
				ids[i] = order.ID
			}
			items, err := exportOrderItems(ctx, q, storeID, ids)
			if err != nil {
				return err
			}
			byOrder := map[uuid.UUID][]dbgen.ExportOrderItemsPageRow{}
			for _, item := range items {
				byOrder[item.OrderID] = append(byOrder[item.OrderID], item)
			}
			for _, order := range orders {
				base := []string{order.ID.String(), order.CustomerID.String(), map[string]string{"male": "男", "female": "女"}[order.Gender], yesNo(order.Designated), order.Status, dateValue(order.BusinessDate), timeValue(order.OpenedAt), order.OpenedByName, idValue(order.CheckoutID), idValue(order.MergedInto), yesNo(order.Backfill)}
				lines := byOrder[order.ID]
				if len(lines) == 0 {
					if err := setRow(sw, line, append(base, "", "", "", "", "", "", "", "")); err != nil {
						return err
					}
					line++
					rows++
					continue
				}
				for _, item := range lines {
					values := append(append([]string{}, base...), item.ID.String(), item.ItemName, item.ItemKind, item.Price, item.Discount, item.PriceSource, timeValue(item.AddedAt), timeValue(item.RemovedAt))
					if err := setRow(sw, line, values); err != nil {
						return err
					}
					line++
					rows++
				}
			}
			if len(orders) < int(exportPageSize) {
				return nil
			}
			id := orders[len(orders)-1].ID
			after = &id
		}
	})
	counts[exportOrders] = rows
	return err
}

func exportOrderItems(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, orderIDs []uuid.UUID) ([]dbgen.ExportOrderItemsPageRow, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	var out []dbgen.ExportOrderItemsPageRow
	var after *uuid.UUID
	for {
		rows, err := q.ExportOrderItemsPage(ctx, dbgen.ExportOrderItemsPageParams{StoreID: storeID, OrderIds: orderIDs, AfterID: after, RowLimit: exportPageSize})
		if err != nil {
			return nil, err
		}
		observeExportPage("order_items", len(rows))
		out = append(out, rows...)
		if len(rows) < int(exportPageSize) {
			return out, nil
		}
		id := rows[len(rows)-1].ID
		after = &id
	}
}

func exportPaymentsSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	headers := []string{"支付ID", "结账ID", "单ID", "方式", "金额", "卡流水ID", "次数流水ID", "红包ID"}
	return streamSheet(f, exportPayments, headers, func(sw *excelize.StreamWriter) error {
		line := 2
		var after *uuid.UUID
		for {
			rows, err := q.ExportOrderPaymentsPage(ctx, dbgen.ExportOrderPaymentsPageParams{StoreID: storeID, AfterID: after, RowLimit: exportPageSize})
			if err != nil {
				return err
			}
			observeExportPage("order_payments", len(rows))
			for _, row := range rows {
				if err := setRow(sw, line, []string{row.ID.String(), row.CheckoutID.String(), row.OrderID.String(), row.Method, row.Amount, idValue(row.CardLedgerID), idValue(row.CardCountLedgerID), idValue(row.RedPacketID)}); err != nil {
					return err
				}
				line++
			}
			counts[exportPayments] += len(rows)
			if len(rows) < int(exportPageSize) {
				return nil
			}
			id := rows[len(rows)-1].ID
			after = &id
		}
	})
}

func exportStaffSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	rows, err := q.ExportStaff(ctx, storeID)
	if err != nil {
		return err
	}
	counts[exportStaffName] = len(rows)
	headers := []string{"员工ID", "姓名", "编号", "在职", "登录账号", "离职时间", "创建时间"}
	return streamSheet(f, exportStaffName, headers, func(sw *excelize.StreamWriter) error {
		for i, row := range rows {
			if err := setRow(sw, i+2, []string{row.ID.String(), row.Name, row.Number, yesNo(row.Active), stringValue(row.LoginAccount), timeValue(row.DeactivatedAt), timeValue(row.CreatedAt)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func exportItemsSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	rows, err := q.ExportItems(ctx, storeID)
	if err != nil {
		return err
	}
	counts[exportItems] = len(rows)
	headers := []string{"项目ID", "名称", "种类", "标准价", "可打折", "手工费金额", "手工费百分比", "启用"}
	return streamSheet(f, exportItems, headers, func(sw *excelize.StreamWriter) error {
		for i, row := range rows {
			if err := setRow(sw, i+2, []string{row.ID.String(), row.Name, row.Kind, row.Price, yesNo(row.Discountable), row.LaborFeeAmountText, row.LaborFeePercentText, yesNo(row.Active)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func exportCardTypesSheet(ctx context.Context, f *excelize.File, q *dbgen.Queries, storeID uuid.UUID, counts map[string]int) error {
	rows, err := q.ExportCardTypes(ctx, storeID)
	if err != nil {
		return err
	}
	items, err := q.ExportCardTypeItems(ctx, storeID)
	if err != nil {
		return err
	}
	byType := map[uuid.UUID][]dbgen.ExportCardTypeItemsRow{}
	for _, item := range items {
		byType[item.CardTypeID] = append(byType[item.CardTypeID], item)
	}
	headers := []string{"卡种ID", "名称", "种类", "折扣率", "有效期方式", "有效期日期", "有效期天数", "启用", "项目", "购买次数", "赠送次数", "单次金额"}
	line := 2
	err = streamSheet(f, exportCardTypes, headers, func(sw *excelize.StreamWriter) error {
		for _, row := range rows {
			days := ""
			if row.ValidityDays != nil {
				days = fmt.Sprint(*row.ValidityDays)
			}
			base := []string{row.ID.String(), row.Name, row.Kind, row.DiscountPercentText, row.ValidityKind, dateValue(row.ValidityDate), days, yesNo(row.Active)}
			lines := byType[row.ID]
			if len(lines) == 0 {
				if err := setRow(sw, line, append(base, "", "", "", "")); err != nil {
					return err
				}
				line++
				counts[exportCardTypes]++
				continue
			}
			for _, item := range lines {
				if err := setRow(sw, line, append(append([]string{}, base...), item.ItemName, fmt.Sprint(item.PaidCount), fmt.Sprint(item.GiftCount), item.UnitPrice)); err != nil {
					return err
				}
				line++
				counts[exportCardTypes]++
			}
		}
		return nil
	})
	return err
}

func dateValue(value pgtype.Date) string {
	if !value.Valid {
		return ""
	}
	return value.Time.Format(time.DateOnly)
}

func timeValue(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return value.Time.Format(time.RFC3339)
}

func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func idValue(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
