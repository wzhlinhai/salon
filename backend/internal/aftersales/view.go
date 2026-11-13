package aftersales

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func view(ctx context.Context, env ops.Env, row dbgen.Aftersale) (*Aftersale, error) {
	out, err := views(ctx, env, []dbgen.Aftersale{row})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

func views(ctx context.Context, env ops.Env, rows []dbgen.Aftersale) ([]Aftersale, error) {
	ids := make([]uuid.UUID, len(rows))
	staffIDs := []uuid.UUID{}
	for i, row := range rows {
		ids[i] = row.ID
		staffIDs = append(staffIDs, row.CreatedBy)
		if row.ApprovedBy != nil {
			staffIDs = append(staffIDs, *row.ApprovedBy)
		}
		if row.RejectedBy != nil {
			staffIDs = append(staffIDs, *row.RejectedBy)
		}
	}
	refunds, err := env.Q.ListAftersaleRefunds(ctx, dbgen.ListAftersaleRefundsParams{StoreID: env.Caller.StoreID, AftersaleIds: ids})
	if err != nil {
		return nil, err
	}
	events, err := env.Q.ListAftersaleEvents(ctx, dbgen.ListAftersaleEventsParams{StoreID: env.Caller.StoreID, AftersaleIds: ids})
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		staffIDs = append(staffIDs, event.ActorID)
	}
	refs, err := staff.References(ctx, env.Q, env.Caller.StoreID, staffIDs)
	if err != nil {
		return nil, err
	}
	byRefund := map[uuid.UUID][]AftersaleRefund{}
	for _, row := range refunds {
		amount := money.Amount{}
		if row.Amount != nil {
			amount = *row.Amount
		}
		byRefund[row.AftersaleID] = append(byRefund[row.AftersaleID], AftersaleRefund{
			ID: row.ID, Method: row.Method, Amount: amount, CardID: row.CardID,
			BusinessDate: row.BusinessDate.Time.Format(time.DateOnly), CreatedAt: row.CreatedAt.Time,
		})
	}
	byEvent := map[uuid.UUID][]AftersaleEvent{}
	for _, row := range events {
		byEvent[row.AftersaleID] = append(byEvent[row.AftersaleID], AftersaleEvent{
			ID: row.ID, Kind: row.Kind, Note: row.Note, Actor: refs[row.ActorID], At: row.At.Time,
		})
	}
	out := make([]Aftersale, 0, len(rows))
	for _, row := range rows {
		lines := []RefundLine{}
		if len(row.RefundLines) > 0 {
			if err := json.Unmarshal(row.RefundLines, &lines); err != nil {
				return nil, err
			}
		}
		item := Aftersale{
			ID: row.ID, OrderID: row.OrderID, Type: row.Type, Reason: row.Reason, Status: row.Status,
			BusinessDate: row.BusinessDate.Time.Format(time.DateOnly), CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time,
			DeductPerformance: row.DeductPerformance, DeductCommission: row.DeductCommission,
			RefundLines: lines, Refunds: byRefund[row.ID], Events: byEvent[row.ID],
		}
		if item.Refunds == nil {
			item.Refunds = []AftersaleRefund{}
		}
		if item.Events == nil {
			item.Events = []AftersaleEvent{}
		}
		if row.ApprovedBy != nil {
			ref := refs[*row.ApprovedBy]
			item.ApprovedBy = &ref
			when := row.ApprovedAt.Time
			item.ApprovedAt = &when
		}
		if row.RejectedBy != nil {
			ref := refs[*row.RejectedBy]
			item.RejectedBy = &ref
			when := row.RejectedAt.Time
			item.RejectedAt = &when
			item.RejectNote = row.RejectNote
		}
		out = append(out, item)
	}
	return out, nil
}
