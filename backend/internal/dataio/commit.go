package dataio

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

var (
	errPhoneNotVisible = errors.New("手机号已属于其他顾客，无权导入")
	errCustomerMerged  = errors.New("该顾客已合并到另一位顾客")
	errCustomerChanged = errors.New("顾客手机号已变化，请重新预览导入")
)

// commit 在一个事务里写入预览通过的顾客、卡、流水和积分；行级错误只跳过该行。
func commit(ctx context.Context, env ops.Env, in *CommitImportInput) (*ImportBatchDetail, error) {
	batch, err := env.Q.LockImportBatch(ctx, dbgen.LockImportBatchParams{StoreID: env.Caller.StoreID, ID: in.BatchID})
	if err != nil {
		return nil, rowError(err)
	}
	if batch.Status != "previewed" {
		return nil, ops.Rule("只能提交预览状态的导入批次")
	}
	if err := env.Q.LockImportStore(ctx, env.Caller.StoreID.String()); err != nil {
		return nil, err
	}
	rows, err := env.Q.ListImportBatchRows(ctx, dbgen.ListImportBatchRowsParams{StoreID: env.Caller.StoreID, BatchID: batch.ID})
	if err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	types, err := cardTypes(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	cardKeys := []string{}
	badCountGroups := map[string]bool{}
	for _, row := range rows {
		if row.Kind == "customer" {
			continue
		}
		if row.Error != nil {
			if row.Kind == "count_package" {
				badCountGroups[row.SourceKey] = true
			}
			continue
		}
		cardKeys = append(cardKeys, row.SourceKey)
	}
	imported := map[string]bool{}
	if len(cardKeys) > 0 {
		keys, err := env.Q.CommittedImportedCardKeys(ctx, dbgen.CommittedImportedCardKeysParams{StoreID: env.Caller.StoreID, Keys: cardKeys})
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			imported[key] = true
		}
	}

	// 先锁住所有可能关联的已有顾客，再申请手机号咨询锁；与改手机号、合并顾客的锁序一致。
	phones := []string{}
	lockIDs := map[uuid.UUID]bool{}
	for _, row := range rows {
		if row.Kind != "customer" || row.Error != nil {
			continue
		}
		if row.Phone != nil {
			phones = append(phones, *row.Phone)
		}
		if row.EntityID != nil {
			lockIDs[*row.EntityID] = true
		}
	}
	found, err := env.Q.FindCustomersByPhones(ctx, phones)
	if err != nil {
		return nil, err
	}
	for _, customer := range found {
		lockIDs[customer.ID] = true
	}
	ordered := make([]uuid.UUID, 0, len(lockIDs))
	for id := range lockIDs {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i][:], ordered[j][:]) < 0 })
	locked := map[uuid.UUID]bool{}
	for _, id := range ordered {
		_, err := env.Q.LockCustomer(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		locked[id] = true
	}
	sort.Strings(phones)
	for i, phone := range phones {
		if i > 0 && phone == phones[i-1] {
			continue
		}
		if err := env.Q.LockCustomerPhone(ctx, phone); err != nil {
			return nil, err
		}
	}

	customerIDs := map[string]uuid.UUID{}
	phoneTargets := map[string]uuid.UUID{}
	var writtenCustomers int32
	for _, row := range rows {
		if row.Kind != "customer" || row.Error != nil {
			continue
		}
		var id uuid.UUID
		var action string
		if row.Phone != nil {
			if target, ok := phoneTargets[*row.Phone]; ok {
				id, action = target, "linked"
			} else {
				id, action, err = resolveCustomer(ctx, env, row, locked)
				if errors.Is(err, errPhoneNotVisible) || errors.Is(err, errCustomerMerged) || errors.Is(err, errCustomerChanged) {
					if err := rowIssue(ctx, env, batch.ID, row.ID, err.Error()); err != nil {
						return nil, err
					}
					continue
				}
				if err != nil {
					return nil, err
				}
				phoneTargets[*row.Phone] = id
			}
		} else {
			id, action, err = resolveCustomer(ctx, env, row, locked)
			if err != nil {
				return nil, err
			}
		}
		if err := record(ctx, env, batch.ID, "customer", id, row.SourceKey, action == "created"); err != nil {
			return nil, err
		}
		customerIDs[row.SourceKey] = id
		writtenCustomers++
		if err := env.Q.UpdateImportBatchRow(ctx, dbgen.UpdateImportBatchRowParams{StoreID: env.Caller.StoreID, BatchID: batch.ID, ID: row.ID, Action: &action, EntityID: &id}); err != nil {
			return nil, err
		}
	}

	// 与开卡一致，先按 UUID 顺序锁住所有要建卡的顾客并复核未合并，再写卡和子表。
	referenced := map[uuid.UUID]bool{}
	for _, row := range rows {
		if row.Kind == "customer" || row.Error != nil {
			continue
		}
		if id, ok := lookupCustomer(row.CustomerSourceKey, customerIDs); ok {
			referenced[id] = true
		}
	}
	customerLockIDs := make([]uuid.UUID, 0, len(referenced))
	for id := range referenced {
		customerLockIDs = append(customerLockIDs, id)
	}
	sort.Slice(customerLockIDs, func(i, j int) bool { return bytes.Compare(customerLockIDs[i][:], customerLockIDs[j][:]) < 0 })
	customerLockErrors := map[uuid.UUID]string{}
	for _, id := range customerLockIDs {
		reason, err := lockImportCustomer(ctx, env, id)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			customerLockErrors[id] = reason
		}
	}

	var writtenStored int32
	var writtenPrincipal, writtenBonus money.Total
	for _, row := range rows {
		if row.Kind != "stored_value" || row.Error != nil {
			continue
		}
		if imported[row.SourceKey] {
			if err := rowIssue(ctx, env, batch.ID, row.ID, "已导入"); err != nil {
				return nil, err
			}
			continue
		}
		customerID, ok := lookupCustomer(row.CustomerSourceKey, customerIDs)
		if !ok {
			if err := rowIssue(ctx, env, batch.ID, row.ID, "来源顾客不存在或未导入"); err != nil {
				return nil, err
			}
			continue
		}
		if err := verifyCardType(row, types, "stored_value"); err != nil {
			if err := rowIssue(ctx, env, batch.ID, row.ID, err.Error()); err != nil {
				return nil, err
			}
			continue
		}
		if reason := customerLockErrors[customerID]; reason != "" {
			if err := rowIssue(ctx, env, batch.ID, row.ID, reason); err != nil {
				return nil, err
			}
			continue
		}
		card, err := env.Q.CreateCard(ctx, dbgen.CreateCardParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: customerID, CardTypeID: *row.CardTypeID, OpenedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}, ExpiresOn: row.ExpiresOn, CreatedBy: env.Caller.StaffID})
		if err != nil {
			return nil, err
		}
		ledger, err := env.Q.AddCardLedger(ctx, dbgen.AddCardLedgerParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, Kind: "import",
			PrincipalBefore: money.Amount{}, PrincipalDelta: row.Principal.Sub(money.Amount{}), PrincipalAfter: row.Principal,
			BonusBefore: money.Amount{}, BonusDelta: row.Bonus.Sub(money.Amount{}), BonusAfter: row.Bonus,
			BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID,
		})
		if err != nil {
			return nil, err
		}
		if err := record(ctx, env, batch.ID, "card", card.ID, row.SourceKey, true); err != nil {
			return nil, err
		}
		imported[row.SourceKey] = true
		if err := record(ctx, env, batch.ID, "card_ledger", ledger.ID, row.SourceKey, true); err != nil {
			return nil, err
		}
		if row.Points != nil && *row.Points > 0 {
			pointsID, err := env.Q.AddImportCardPoints(ctx, dbgen.AddImportCardPointsParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, Delta: *row.Points, After: *row.Points, CardLedgerID: &ledger.ID, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
			if err != nil {
				return nil, err
			}
			if err := record(ctx, env, batch.ID, "card_points_ledger", pointsID, row.SourceKey, true); err != nil {
				return nil, err
			}
		}
		action := "created"
		if err := env.Q.UpdateImportBatchRow(ctx, dbgen.UpdateImportBatchRowParams{StoreID: env.Caller.StoreID, BatchID: batch.ID, ID: row.ID, Action: &action, EntityID: &card.ID}); err != nil {
			return nil, err
		}
		writtenStored++
		writtenPrincipal = writtenPrincipal.AddProduct(row.Principal, 1)
		writtenBonus = writtenBonus.AddProduct(row.Bonus, 1)
	}

	groups := map[string][]dbgen.ImportBatchRow{}
	order := []string{}
	for _, row := range rows {
		if row.Kind != "count_package" || row.Error != nil {
			continue
		}
		if _, ok := groups[row.SourceKey]; !ok {
			order = append(order, row.SourceKey)
		}
		groups[row.SourceKey] = append(groups[row.SourceKey], row)
	}
	var writtenCount int32
	for _, key := range order {
		group := groups[key]
		first := group[0]
		if badCountGroups[key] {
			for _, row := range group {
				if err := rowIssue(ctx, env, batch.ID, row.ID, "同一来源卡号的其他项目有错误"); err != nil {
					return nil, err
				}
			}
			continue
		}
		if imported[key] {
			for _, row := range group {
				if err := rowIssue(ctx, env, batch.ID, row.ID, "已导入"); err != nil {
					return nil, err
				}
			}
			continue
		}
		customerID, ok := lookupCustomer(first.CustomerSourceKey, customerIDs)
		if !ok {
			for _, row := range group {
				if err := rowIssue(ctx, env, batch.ID, row.ID, "来源顾客不存在或未导入"); err != nil {
					return nil, err
				}
			}
			continue
		}
		if reason := customerLockErrors[customerID]; reason != "" {
			for _, row := range group {
				if err := rowIssue(ctx, env, batch.ID, row.ID, reason); err != nil {
					return nil, err
				}
			}
			continue
		}
		failed := false
		for _, row := range group {
			if err := verifyCardType(row, types, "count_package"); err != nil {
				failed = true
				break
			}
		}
		if failed {
			reason := "同一来源卡号的其他项目有错误"
			for _, row := range group {
				if err := rowIssue(ctx, env, batch.ID, row.ID, reason); err != nil {
					return nil, err
				}
			}
			continue
		}
		card, err := env.Q.CreateCard(ctx, dbgen.CreateCardParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: customerID, CardTypeID: *first.CardTypeID, OpenedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}, ExpiresOn: first.ExpiresOn, CreatedBy: env.Caller.StaffID})
		if err != nil {
			return nil, err
		}
		ledger, err := env.Q.AddCardLedger(ctx, dbgen.AddCardLedgerParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, Kind: "import",
			PrincipalBefore: money.Amount{}, PrincipalDelta: money.Amount{}.Sub(money.Amount{}), PrincipalAfter: money.Amount{},
			BonusBefore: money.Amount{}, BonusDelta: money.Amount{}.Sub(money.Amount{}), BonusAfter: money.Amount{},
			BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID,
		})
		if err != nil {
			return nil, err
		}
		if err := record(ctx, env, batch.ID, "card", card.ID, first.SourceKey, true); err != nil {
			return nil, err
		}
		imported[first.SourceKey] = true
		if err := record(ctx, env, batch.ID, "card_ledger", ledger.ID, first.SourceKey, true); err != nil {
			return nil, err
		}
		for _, row := range group {
			info := types[stringValue(row.CardTypeName)]
			item := info.items[stringValue(row.ItemName)]
			if err := env.Q.AddCardItem(ctx, dbgen.AddCardItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, ItemID: *row.ItemID, UnitPrice: item.UnitPrice, PaidCountOpened: *row.PaidCount, GiftCountOpened: *row.GiftCount}); err != nil {
				return nil, err
			}
			countID, err := env.Q.AddCardCountEntry(ctx, dbgen.AddCardCountEntryParams{
				TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, ItemID: *row.ItemID, Kind: "import",
				PaidBefore: 0, PaidDelta: *row.PaidCount, PaidAfter: *row.PaidCount,
				GiftBefore: 0, GiftDelta: *row.GiftCount, GiftAfter: *row.GiftCount,
				BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID,
			})
			if err != nil {
				return nil, err
			}
			if err := record(ctx, env, batch.ID, "card_item", card.ID, row.ItemID.String(), true); err != nil {
				return nil, err
			}
			if err := record(ctx, env, batch.ID, "card_count_ledger", countID, row.ItemID.String(), true); err != nil {
				return nil, err
			}
		}
		action := "created"
		for _, row := range group {
			if err := env.Q.UpdateImportBatchRow(ctx, dbgen.UpdateImportBatchRowParams{StoreID: env.Caller.StoreID, BatchID: batch.ID, ID: row.ID, Action: &action, EntityID: &card.ID}); err != nil {
				return nil, err
			}
		}
		writtenCount++
	}

	if err := env.Q.MarkImportBatchCommitted(ctx, dbgen.MarkImportBatchCommittedParams{
		ID: batch.ID, CommittedBy: &env.Caller.StaffID, CommittedAt: pgtype.Timestamptz{Time: env.Now, Valid: true},
		WrittenCustomers: writtenCustomers, WrittenStoredCards: writtenStored, WrittenPrincipal: writtenPrincipal.Format(), WrittenBonus: writtenBonus.Format(), WrittenCountCards: writtenCount,
	}); err != nil {
		return nil, err
	}
	out, err := loadDetail(ctx, env, env.Q, env.Caller.StoreID, batch.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "commit_import", audit.ImportBatchTarget(batch.ID), nil, out.ImportBatch); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveCustomer 按租户手机号查询并复用查看范围，预览和提交使用同一条查询。
func resolveCustomer(ctx context.Context, env ops.Env, row dbgen.ImportBatchRow, locked map[uuid.UUID]bool) (uuid.UUID, string, error) {
	if row.Phone != nil {
		found, err := env.Q.FindCustomersByPhones(ctx, []string{*row.Phone})
		if err != nil {
			return uuid.Nil, "", err
		}
		if len(found) > 0 {
			if !locked[found[0].ID] {
				return uuid.Nil, "", errCustomerChanged
			}
			if !customers.CanView(env.Caller, found[0]) {
				return uuid.Nil, "", errPhoneNotVisible
			}
			return found[0].ID, "linked", nil
		}
		// 预览时链接到的顾客若在提交前被合并，按预览记录复核并报错，而不是悄悄新建一份。
		if row.EntityID != nil {
			linked, err := env.Q.GetCustomer(ctx, *row.EntityID)
			if err == nil {
				if !locked[linked.ID] {
					return uuid.Nil, "", errCustomerChanged
				}
				if !customers.CanView(env.Caller, linked) {
					return uuid.Nil, "", errPhoneNotVisible
				}
				if linked.MergedInto != nil {
					return uuid.Nil, "", errCustomerMerged
				}
				return linked.ID, "linked", nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, "", err
			}
		}
	}
	created, err := env.Q.CreateCustomer(ctx, dbgen.CreateCustomerParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: row.Name, Phone: row.Phone, Gender: row.Gender, CreatedBy: env.Caller.StaffID})
	if errors.Is(err, pgx.ErrNoRows) && row.Phone != nil {
		found, findErr := env.Q.FindCustomersByPhones(ctx, []string{*row.Phone})
		if findErr != nil || len(found) == 0 {
			return uuid.Nil, "", findErr
		}
		if !locked[found[0].ID] {
			return uuid.Nil, "", errCustomerChanged
		}
		if !customers.CanView(env.Caller, found[0]) {
			return uuid.Nil, "", errPhoneNotVisible
		}
		return found[0].ID, "linked", nil
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	locked[created.ID] = true
	return created.ID, "created", nil
}

func lockImportCustomer(ctx context.Context, env ops.Env, id uuid.UUID) (string, error) {
	_, err := customers.LockCardOpening(ctx, env, id)
	if err == nil {
		return "", nil
	}
	var rule *ops.RuleError
	if errors.As(err, &rule) {
		return rule.Message, nil
	}
	if errors.Is(err, ops.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return "来源顾客不存在或未导入", nil
	}
	return "", err
}

func record(ctx context.Context, env ops.Env, batchID uuid.UUID, kind string, entityID uuid.UUID, sourceKey string, created bool) error {
	return env.Q.AddImportBatchRecord(ctx, dbgen.AddImportBatchRecordParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BatchID: batchID, Kind: kind, EntityID: entityID, SourceKey: sourceKey, Created: created})
}

func rowIssue(ctx context.Context, env ops.Env, batchID, rowID uuid.UUID, reason string) error {
	return env.Q.UpdateImportBatchRow(ctx, dbgen.UpdateImportBatchRowParams{StoreID: env.Caller.StoreID, BatchID: batchID, ID: rowID, Error: &reason})
}

func lookupCustomer(key *string, ids map[string]uuid.UUID) (uuid.UUID, bool) {
	if key == nil {
		return uuid.Nil, false
	}
	id, ok := ids[*key]
	return id, ok
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func verifyCardType(row dbgen.ImportBatchRow, types map[string]cardTypeInfo, want string) error {
	name := stringValue(row.CardTypeName)
	info, ok := types[name]
	if !ok {
		return errors.New("卡种未配置: " + name)
	}
	if want == "stored_value" && info.row.Kind != "stored_value" {
		return errors.New("卡种未配置: " + name)
	}
	if want == "count_package" && info.row.Kind != "count" && info.row.Kind != "package" {
		return errors.New("卡种未配置: " + name)
	}
	if row.CardTypeID == nil || *row.CardTypeID != info.row.ID {
		return errors.New("卡种未配置: " + name)
	}
	if row.ItemID != nil {
		if _, ok := info.items[stringValue(row.ItemName)]; !ok {
			return errors.New("卡种未配置: " + name)
		}
	}
	return nil
}
