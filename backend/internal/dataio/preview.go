package dataio

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type cardTypeInfo struct {
	row   dbgen.CardType
	items map[string]dbgen.CardTypeItem
}

func cardTypes(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID) (map[string]cardTypeInfo, error) {
	rows, err := q.ListCardTypes(ctx, storeID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	out := make(map[string]cardTypeInfo, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		out[row.Name] = cardTypeInfo{row: row, items: map[string]dbgen.CardTypeItem{}}
	}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := q.ListCardTypeItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		x := item.CardTypeItem
		for name, info := range out {
			if info.row.ID == x.CardTypeID {
				info.items[item.ItemName] = x
				out[name] = info
				break
			}
		}
	}
	return out, nil
}

// Preview 解析中间 Excel 并把预览结果保存为一批导入；不写顾客和卡。
func Preview(ctx context.Context, pool *pgxpool.Pool, caller ops.Caller, now time.Time, fileName string, r io.Reader) (*ImportBatchDetail, error) {
	if !caller.Has(perm.DataImport) {
		return nil, ops.ErrForbidden
	}
	file, err := parseWorkbook(r)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(fileName) == "" {
		fileName = "导入.xlsx"
	}
	var out *ImportBatchDetail
	err = db.InTenant(ctx, pool, caller.TenantID, func(q *dbgen.Queries) error {
		env := ops.Env{Now: now, Caller: caller, Q: q}
		types, err := cardTypes(ctx, q, caller.StoreID)
		if err != nil {
			return err
		}
		phones := make([]string, 0, len(file.Customers))
		for _, c := range file.Customers {
			if c.Phone != nil {
				phones = append(phones, *c.Phone)
			}
		}
		existing := map[string]dbgen.Customer{}
		if len(phones) > 0 {
			rows, err := q.FindCustomersByPhones(ctx, phones)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.Phone != nil {
					existing[*row.Phone] = row
				}
			}
		}

		batch, err := q.CreateImportBatch(ctx, dbgen.CreateImportBatchParams{TenantID: caller.TenantID, StoreID: caller.StoreID, FileName: fileName, CreatedBy: caller.StaffID})
		if err != nil {
			return err
		}

		customerByKey := map[string]bool{}
		seenPhone := map[string]uuid.UUID{}
		storedKeys, countKeys := map[string]bool{}, map[string]bool{}
		type countGroup struct {
			customerKey, typeName string
			expires               *time.Time
		}
		countGroups := map[string]countGroup{}
		seenCountItems := map[string]bool{}

		for _, c := range file.Customers {
			params := dbgen.AddImportBatchRowParams{
				TenantID: caller.TenantID, StoreID: caller.StoreID, BatchID: batch.ID,
				Sheet: sheetCustomers, RowNumber: c.RowNumber, Kind: "customer", SourceKey: c.SourceKey,
				Name: c.Name, Phone: c.Phone, Gender: c.Gender, Principal: money.Amount{}, Bonus: money.Amount{},
			}
			message := c.Err
			if _, dup := customerByKey[c.SourceKey]; dup && message == "" {
				message = "来源顾客ID重复"
			} else if message == "" {
				customerByKey[c.SourceKey] = true
			}
			if message == "" {
				switch {
				case c.Phone != nil:
					if found, ok := existing[*c.Phone]; ok && !customers.CanView(caller, found) {
						message = "手机号已属于其他顾客，无权导入"
					} else if ok {
						params.Action = ptr("linked")
						params.EntityID = &found.ID
					} else if _, ok := seenPhone[*c.Phone]; ok {
						params.Action = ptr("linked")
					} else {
						params.Action = ptr("created")
						seenPhone[*c.Phone] = uuid.Nil
					}
				default:
					params.Action = ptr("created")
				}
			}
			if message != "" {
				params.Error = &message
			}
			if _, err := q.AddImportBatchRow(ctx, params); err != nil {
				return err
			}
		}

		for _, c := range file.Stored {
			params := dbgen.AddImportBatchRowParams{
				TenantID: caller.TenantID, StoreID: caller.StoreID, BatchID: batch.ID,
				Sheet: sheetStoredCards, RowNumber: c.RowNumber, Kind: "stored_value", SourceKey: c.SourceKey,
				CustomerSourceKey: ptr(c.CustomerKey), Principal: c.Principal, Bonus: c.Bonus,
			}
			message := c.Err
			if _, ok := storedKeys[c.SourceKey]; ok && message == "" {
				message = "来源卡号重复"
			}
			storedKeys[c.SourceKey] = true
			typeName := optionalText(c.CardTypeName)
			if typeName != nil {
				params.CardTypeName = typeName
			}
			if message == "" && !customerByKey[c.CustomerKey] {
				message = "来源顾客不存在或未导入"
			}
			if message == "" {
				info, ok := types[c.CardTypeName]
				if !ok || info.row.Kind != "stored_value" {
					message = "卡种未配置: " + c.CardTypeName
				} else {
					params.CardTypeID = &info.row.ID
				}
			}
			if message == "" {
				points := c.Points
				params.Points = &points
				if c.ExpiresOn != nil {
					params.ExpiresOn = pgtype.Date{Time: *c.ExpiresOn, Valid: true}
				}
				params.Action = ptr("created")
			}
			if message != "" {
				params.Error = &message
			}
			if _, err := q.AddImportBatchRow(ctx, params); err != nil {
				return err
			}
		}

		type countRow struct {
			id        uuid.UUID
			sourceKey string
			params    dbgen.AddImportBatchRowParams
		}
		countRows := []countRow{}
		for _, c := range file.Counts {
			params := dbgen.AddImportBatchRowParams{
				TenantID: caller.TenantID, StoreID: caller.StoreID, BatchID: batch.ID,
				Sheet: sheetCountCards, RowNumber: c.RowNumber, Kind: "count_package", SourceKey: c.SourceKey,
				CustomerSourceKey: ptr(c.CustomerKey), Principal: money.Amount{}, Bonus: money.Amount{},
			}
			message := c.Err
			if storedKeys[c.SourceKey] && message == "" {
				message = "来源卡号与储值卡重复"
			}
			itemKey := c.SourceKey + "\x00" + c.ItemName
			if seenCountItems[itemKey] && message == "" {
				message = "同一来源卡号的项目重复"
			}
			seenCountItems[itemKey] = true
			typeName := optionalText(c.CardTypeName)
			if typeName != nil {
				params.CardTypeName = typeName
			}
			itemName := optionalText(c.ItemName)
			if itemName != nil {
				params.ItemName = itemName
			}
			paid, gift := c.Paid, c.Gift
			params.PaidCount, params.GiftCount = &paid, &gift
			if message == "" && !customerByKey[c.CustomerKey] {
				message = "来源顾客不存在或未导入"
			}
			if message == "" {
				group, ok := countGroups[c.SourceKey]
				if ok && (group.customerKey != c.CustomerKey || group.typeName != c.CardTypeName || !sameDate(group.expires, c.ExpiresOn)) {
					message = "同一来源卡号的顾客、卡种或到期日不一致"
				} else if !ok {
					countGroups[c.SourceKey] = countGroup{customerKey: c.CustomerKey, typeName: c.CardTypeName, expires: c.ExpiresOn}
				}
			}
			if message == "" {
				info, ok := types[c.CardTypeName]
				if !ok || (info.row.Kind != "count" && info.row.Kind != "package") {
					message = "卡种未配置: " + c.CardTypeName
				} else if item, ok := info.items[c.ItemName]; !ok {
					message = "卡种未配置: " + c.CardTypeName
				} else {
					params.CardTypeID, params.ItemID = &info.row.ID, &item.ItemID
				}
			}
			if message == "" {
				if c.ExpiresOn != nil {
					params.ExpiresOn = pgtype.Date{Time: *c.ExpiresOn, Valid: true}
				}
				params.Action = ptr("created")
			}
			if message != "" {
				params.Error = &message
			}
			id, err := q.AddImportBatchRow(ctx, params)
			if err != nil {
				return err
			}
			countRows = append(countRows, countRow{id: id, sourceKey: c.SourceKey, params: params})
		}
		// 同一个来源卡号是一个套餐组：任意项目有错时整组标错，避免提交时留下没有 entity_id 的 created 行。
		countGroupsByKey := map[string][]countRow{}
		for _, row := range countRows {
			countGroupsByKey[row.sourceKey] = append(countGroupsByKey[row.sourceKey], row)
		}
		for key, group := range countGroupsByKey {
			invalid := false
			for _, row := range group {
				if row.params.Error != nil {
					invalid = true
					break
				}
			}
			if invalid {
				reason := "同一来源卡号的其他项目有错误"
				for _, row := range group {
					if err := q.UpdateImportBatchRow(ctx, dbgen.UpdateImportBatchRowParams{StoreID: caller.StoreID, BatchID: batch.ID, ID: row.id, Error: &reason}); err != nil {
						return err
					}
				}
				continue
			}
			countKeys[key] = true
		}

		keys := make([]string, 0, len(storedKeys)+len(countKeys))
		for key := range storedKeys {
			keys = append(keys, key)
		}
		for key := range countKeys {
			keys = append(keys, key)
		}
		if len(keys) > 0 {
			imported, err := q.CommittedImportedCardKeys(ctx, dbgen.CommittedImportedCardKeysParams{StoreID: caller.StoreID, Keys: keys})
			if err != nil {
				return err
			}
			if len(imported) > 0 {
				seen := map[string]bool{}
				for _, key := range imported {
					seen[key] = true
				}
				rows, err := q.ListImportBatchRows(ctx, dbgen.ListImportBatchRowsParams{StoreID: caller.StoreID, BatchID: batch.ID})
				if err != nil {
					return err
				}
				for _, row := range rows {
					if row.Kind == "customer" || row.Error != nil || !seen[row.SourceKey] {
						continue
					}
					reason := "已导入"
					if err := q.UpdateImportBatchRow(ctx, dbgen.UpdateImportBatchRowParams{StoreID: caller.StoreID, BatchID: batch.ID, ID: row.ID, Error: &reason}); err != nil {
						return err
					}
				}
			}
		}

		rows, err := q.ListImportBatchRows(ctx, dbgen.ListImportBatchRowsParams{StoreID: caller.StoreID, BatchID: batch.ID})
		if err != nil {
			return err
		}
		fileTotals := zeroTotals()
		var principal, bonus money.Total
		counted := map[string]bool{}
		for _, row := range rows {
			if row.Error != nil {
				continue
			}
			switch row.Kind {
			case "customer":
				fileTotals.Customers++
			case "stored_value":
				fileTotals.StoredValueCards++
				addAmount(&principal, row.Principal)
				addAmount(&bonus, row.Bonus)
			case "count_package":
				if !counted[row.SourceKey] {
					counted[row.SourceKey] = true
					fileTotals.CountPackageCards++
				}
			}
		}
		fileTotals.Principal, fileTotals.Bonus = totalString(principal), totalString(bonus)
		declared := ImportTotals{
			Customers:         file.Summary.Customers,
			StoredValueCards:  file.Summary.StoredCards,
			Principal:         totalString(money.Total{}.AddProduct(file.Summary.Principal, 1)),
			Bonus:             totalString(money.Total{}.AddProduct(file.Summary.Bonus, 1)),
			CountPackageCards: file.Summary.CountPackageCards,
		}
		if err := q.SetImportBatchFileTotals(ctx, dbgen.SetImportBatchFileTotalsParams{
			ID: batch.ID, DeclaredCustomers: declared.Customers, DeclaredStoredCards: declared.StoredValueCards,
			DeclaredPrincipal: declared.Principal, DeclaredBonus: declared.Bonus, DeclaredCountCards: declared.CountPackageCards,
			FileCustomers: fileTotals.Customers, FileStoredCards: fileTotals.StoredValueCards,
			FilePrincipal: fileTotals.Principal, FileBonus: fileTotals.Bonus, FileCountCards: fileTotals.CountPackageCards,
		}); err != nil {
			return err
		}
		out, err = loadDetail(ctx, env, q, caller.StoreID, batch.ID)
		if err != nil {
			return err
		}
		return audit.Record(ctx, env, "preview_import", audit.ImportBatchTarget(batch.ID), nil, out.ImportBatch)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func sameDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
