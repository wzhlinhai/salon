package commission

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func view(ctx context.Context, q *dbgen.Queries, row dbgen.CommissionSchemeVersion) (*CommissionSchemeVersionView, error) {
	var scheme CommissionScheme
	if err := json.Unmarshal(row.Scheme, &scheme); err != nil {
		return nil, err
	}
	staffIDs := append([]uuid.UUID{row.SavedBy}, scheme.ExcludedStaff...)
	positions, items, categories := []uuid.UUID{}, []uuid.UUID{}, []uuid.UUID{}
	for _, r := range scheme.Rules {
		for _, c := range r.Cells {
			if c.PositionID != nil {
				positions = append(positions, *c.PositionID)
			}
		}
	}
	for _, o := range scheme.Overrides {
		if o.StaffID != nil {
			staffIDs = append(staffIDs, *o.StaffID)
		}
		if o.ItemID != nil {
			items = append(items, *o.ItemID)
		}
		if o.PositionID != nil {
			positions = append(positions, *o.PositionID)
		}
		if o.CategoryID != nil {
			categories = append(categories, *o.CategoryID)
		}
	}
	staffRefs, err := staff.References(ctx, q, row.StoreID, staffIDs)
	if errors.Is(err, ops.ErrNotFound) {
		return nil, ops.Rule("提成方案的员工必须属于本店")
	}
	if err != nil {
		return nil, err
	}
	positionRefs, err := staff.PositionReferences(ctx, q, row.StoreID, positions)
	if errors.Is(err, ops.ErrNotFound) {
		return nil, ops.Rule("提成方案的岗位必须属于本店")
	}
	if err != nil {
		return nil, err
	}
	itemRefs, err := catalog.References(ctx, q, row.StoreID, items)
	if errors.Is(err, ops.ErrNotFound) {
		return nil, ops.Rule("提成方案的项目必须属于本店")
	}
	if err != nil {
		return nil, err
	}
	categoryRefs, err := catalog.CategoryReferences(ctx, q, row.StoreID, categories)
	if errors.Is(err, ops.ErrNotFound) {
		return nil, ops.Rule("提成方案的类别必须属于本店")
	}
	if err != nil {
		return nil, err
	}
	out := &CommissionSchemeVersionView{CommissionSchemeVersionSummary: CommissionSchemeVersionSummary{ID: row.ID, Version: row.Version, EffectiveFrom: row.EffectiveFrom.Time.Format(settings.DateLayout), SavedBy: staffRefs[row.SavedBy], SavedAt: row.SavedAt.Time}, Scheme: CommissionSchemeView{Rules: []CommissionRuleView{}, Overrides: []CommissionOverrideView{}, ExcludedStaff: []CommissionExcludedStaffView{}}}
	for _, r := range scheme.Rules {
		rv := CommissionRuleView{Source: r.Source, Base: r.Base, ByPosition: r.ByPosition, ByDesignated: r.ByDesignated, ByMember: r.ByMember, Cells: []CommissionCellView{}}
		for _, c := range r.Cells {
			cv := CommissionCellView{Cell: c}
			if c.PositionID != nil {
				cv.Position = new(positionRefs[*c.PositionID])
			}
			rv.Cells = append(rv.Cells, cv)
		}
		out.Scheme.Rules = append(out.Scheme.Rules, rv)
	}
	for _, o := range scheme.Overrides {
		ov := CommissionOverrideView{Override: o}
		if o.StaffID != nil {
			ov.Staff = new(staffRefs[*o.StaffID])
		}
		if o.ItemID != nil {
			ov.Item = new(itemRefs[*o.ItemID])
		}
		if o.PositionID != nil {
			ov.Position = new(positionRefs[*o.PositionID])
		}
		if o.CategoryID != nil {
			ov.Category = new(categoryRefs[*o.CategoryID])
		}
		out.Scheme.Overrides = append(out.Scheme.Overrides, ov)
	}
	for _, id := range scheme.ExcludedStaff {
		out.Scheme.ExcludedStaff = append(out.Scheme.ExcludedStaff, CommissionExcludedStaffView{StaffID: id, Staff: staffRefs[id]})
	}
	return out, nil
}
