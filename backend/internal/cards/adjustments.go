package cards

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func adjustCard(ctx context.Context, env ops.Env, in *AdjustCardInput) (*Card, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 200 {
		return nil, ops.Rule("原因须为一到二百字")
	}
	return changeOne(ctx, env, "adjust_card", in.CardID, func(c cardChange, day time.Time) error {
		card, before := c.row, c.before
		pointsChanged := in.Points != nil && *in.Points != before.Points
		if in.Points != nil && (*in.Points < 0 || *in.Points > 99999999) {
			return ops.Rule("积分须为 0 到 99999999")
		}
		principal, bonus := before.Principal, before.Bonus
		if in.Principal != nil {
			principal = *in.Principal
		}
		if in.Bonus != nil {
			bonus = *in.Bonus
		}
		balanceChanged := principal.Cmp(before.Principal) != 0 || bonus.Cmp(before.Bonus) != 0
		expires := card.ExpiresOn
		if in.Expiry != nil {
			expires = pgtype.Date{}
			if in.Expiry.ExpiresOn != nil {
				date, err := time.Parse(time.DateOnly, *in.Expiry.ExpiresOn)
				if err != nil {
					return ops.Rule("有效期日期无效")
				}
				// 调整单用于更正历史，有效期可以早于当前营业日。
				expires = pgtype.Date{Time: date, Valid: true}
			}
		}
		expiryChanged := expires.Valid != card.ExpiresOn.Valid || (expires.Valid && !expires.Time.Equal(card.ExpiresOn.Time))
		typeID := card.CardTypeID
		var typeBefore, typeAfter *uuid.UUID
		if in.CardTypeID != nil && *in.CardTypeID != card.CardTypeID {
			// 固定锁序：卡 → 新卡类型；开卡只锁类型，类型编辑也只锁类型。
			ct, err := env.Q.ShareCardType(ctx, dbgen.ShareCardTypeParams{StoreID: env.Caller.StoreID, ID: *in.CardTypeID})
			if err != nil {
				return rowError(err)
			}
			old, err := env.Q.GetCardType(ctx, dbgen.GetCardTypeParams{StoreID: env.Caller.StoreID, ID: card.CardTypeID})
			if err != nil {
				return err
			}
			if old.Kind != ct.Kind {
				return ops.Rule("卡类型必须同类")
			}
			if !ct.Active {
				return ops.Rule("卡类型已停用")
			}
			typeID = ct.ID
			typeBefore, typeAfter = &card.CardTypeID, &typeID
		}
		changes := []dbgen.AddCardCountEntryParams{}
		seen := map[uuid.UUID]bool{}
		for _, target := range in.Counts {
			if seen[target.ItemID] || target.Paid < 0 || target.Gift < 0 {
				return ops.Rule("次数目标不能重复或为负")
			}
			seen[target.ItemID] = true
			found := false
			for _, item := range before.Items {
				if item.ItemID == target.ItemID {
					found = true
					if target.Paid != item.PaidRemaining || target.Gift != item.GiftRemaining {
						changes = append(changes, dbgen.AddCardCountEntryParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, ItemID: target.ItemID, Kind: "adjust", PaidBefore: item.PaidRemaining, PaidDelta: target.Paid - item.PaidRemaining, PaidAfter: target.Paid, GiftBefore: item.GiftRemaining, GiftDelta: target.Gift - item.GiftRemaining, GiftAfter: target.Gift, ActorID: env.Caller.StaffID})
					}
				}
			}
			if !found {
				return ops.Rule("项目不在卡上")
			}
		}
		if !balanceChanged && !expiryChanged && typeAfter == nil && len(changes) == 0 && !pointsChanged {
			return ops.Rule("调整单没有任何改动")
		}
		var ledgerID *uuid.UUID
		if balanceChanged {
			row, err := writeBalance(ctx, env, card.ID, "adjust", before.Principal, before.Bonus, principal.Sub(before.Principal), bonus.Sub(before.Bonus), day)
			if err != nil {
				return err
			}
			ledgerID = &row.ID
		}
		if expiryChanged || typeAfter != nil {
			if err := env.Q.UpdateAdjustedCard(ctx, dbgen.UpdateAdjustedCardParams{ID: card.ID, ExpiresOn: expires, CardTypeID: typeID}); err != nil {
				return err
			}
		}
		var expiresBefore, expiresAfter pgtype.Date
		if expiryChanged {
			expiresBefore, expiresAfter = card.ExpiresOn, expires
		}
		adjustment, err := env.Q.AddCardAdjustment(ctx, dbgen.AddCardAdjustmentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: card.ID, Reason: in.Reason, ExpiresBefore: expiresBefore, ExpiresAfter: expiresAfter, ExpiryChanged: expiryChanged, CardTypeBefore: typeBefore, CardTypeAfter: typeAfter, LedgerID: ledgerID, CountsChanged: len(changes) > 0, PointsChanged: pointsChanged, BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID})
		if err != nil {
			return err
		}
		if pointsChanged {
			if err := adjustPoints(ctx, env, card.ID, adjustment.ID, before.Points, *in.Points, day); err != nil {
				return err
			}
		}
		for _, change := range changes {
			change.AdjustmentID = &adjustment.ID
			change.BusinessDate = pgtype.Date{Time: day, Valid: true}
			if err := env.Q.AddCardCountEntry(ctx, change); err != nil {
				return err
			}
		}
		return nil
	})
}

func listCardAdjustments(ctx context.Context, env ops.Env, in *ListCardAdjustmentsInput) (*CardAdjustmentList, error) {
	card, err := env.Q.GetCard(ctx, dbgen.GetCardParams{StoreID: env.Caller.StoreID, ID: in.CardID})
	if err != nil {
		return nil, rowError(err)
	}
	if err := canView(ctx, env, card.CustomerID); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListCardAdjustments(ctx, dbgen.ListCardAdjustmentsParams{CardID: card.ID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.CardAdjustment.ID
	}
	counts, err := env.Q.AdjustmentCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID][]CardCountEntry{}
	for _, r := range counts {
		l := r.CardCountLedger
		byID[*l.AdjustmentID] = append(byID[*l.AdjustmentID], countEntry(l, r.ItemName, "", ""))
	}
	out := &CardAdjustmentList{Entries: []CardAdjustment{}}
	for _, r := range rows {
		a := r.CardAdjustment
		item := CardAdjustment{ID: a.ID, Reason: a.Reason, BusinessDate: a.BusinessDate.Time.Format(time.DateOnly), At: a.At.Time, Actor: CardLedgerActor{ID: a.ActorID, Name: r.ActorName}}
		item.Counts = byID[a.ID]
		if item.Counts == nil {
			item.Counts = []CardCountEntry{}
		}
		for i := range item.Counts {
			item.Counts[i].Reason = a.Reason
			item.Counts[i].Actor = item.Actor
		}
		if a.ExpiryChanged {
			item.ExpiryBefore = &CardExpiry{ExpiresOn: dateView(a.ExpiresBefore)}
			item.ExpiryAfter = &CardExpiry{ExpiresOn: dateView(a.ExpiresAfter)}
		}
		if a.CardTypeBefore != nil {
			item.CardTypeBefore = &CardAdjustmentType{ID: *a.CardTypeBefore, Name: *r.TypeBeforeName}
			item.CardTypeAfter = &CardAdjustmentType{ID: *a.CardTypeAfter, Name: *r.TypeAfterName}
		}
		if a.LedgerID != nil {
			item.Balance = &CardAdjustmentBalance{PrincipalBefore: r.PrincipalBefore, PrincipalDelta: r.PrincipalDelta, PrincipalAfter: r.PrincipalAfter, BonusBefore: r.BonusBefore, BonusDelta: r.BonusDelta, BonusAfter: r.BonusAfter}
		}
		out.Entries = append(out.Entries, item)
	}
	return out, nil
}
