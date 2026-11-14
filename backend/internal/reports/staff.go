package reports

import (
	"context"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/commission"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

type staffAccum struct {
	staff            *staff.Ref
	item             money.SignedTotal
	consume          money.SignedTotal
	labor            money.SignedTotal
	sale             money.SignedTotal
	cash             money.SignedTotal
	card             money.SignedTotal
	guestTotal       int64
	guestDesignated  int64
	guestNot         int64
	guestMale        int64
	guestFemale      int64
	serviceItemLines int64
	commission       money.SignedTotal
}

func getStaffReport(ctx context.Context, env ops.Env, in *StaffReportInput) (*StaffReport, error) {
	q, err := staffDateRange(env.Caller, in.From, in.To, in.StaffID)
	if err != nil {
		return nil, err
	}
	out := &StaffReport{Rows: []StaffReportRow{}, Totals: StaffReportRow{}}
	if q.Empty {
		return out, nil
	}
	from, to := pgDate(q.From), pgDate(q.To)
	rows := map[string]*staffAccum{}
	get := func(id *uuid.UUID) *staffAccum {
		key := ""
		if id != nil {
			key = id.String()
		}
		if rows[key] == nil {
			acc := &staffAccum{}
			if id != nil {
				acc.staff = &staff.Ref{ID: *id}
			}
			rows[key] = acc
		}
		return rows[key]
	}
	performance, err := env.Q.StaffPerformance(ctx, dbgen.StaffPerformanceParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to, StaffID: q.StaffID})
	if err != nil {
		return nil, err
	}
	for _, row := range performance {
		acc := get(row.StaffID)
		switch row.Kind {
		case "item":
			if err := addSigned(&acc.item, row.Amount); err != nil {
				return nil, err
			}
			if err := addSigned(&acc.cash, row.CashPerformance); err != nil {
				return nil, err
			}
			if err := addSigned(&acc.card, row.CardPerformance); err != nil {
				return nil, err
			}
		case "consume":
			if err := addSigned(&acc.consume, row.Amount); err != nil {
				return nil, err
			}
		case "labor":
			if err := addSigned(&acc.labor, row.Amount); err != nil {
				return nil, err
			}
		case "sale":
			if err := addSigned(&acc.sale, row.Amount); err != nil {
				return nil, err
			}
		}
	}
	guests, err := env.Q.StaffGuestVisits(ctx, dbgen.StaffGuestVisitsParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to, StaffID: q.StaffID})
	if err != nil {
		return nil, err
	}
	for _, row := range guests {
		acc := get(row.StaffID)
		if err := addGuest(acc, row.Total, row.Designated, row.NotDesignated, row.Male, row.Female); err != nil {
			return nil, err
		}
	}
	lines, err := env.Q.StaffItemLines(ctx, dbgen.StaffItemLinesParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to, StaffID: q.StaffID})
	if err != nil {
		return nil, err
	}
	for _, row := range lines {
		get(row.StaffID).serviceItemLines += row.Lines
	}
	commissions, err := commission.Commissions(ctx, env.Q, env.Caller.StoreID, q.From, q.To, q.StaffID)
	if err != nil {
		return nil, err
	}
	for _, c := range commissions {
		acc := get(&c.Staff.ID)
		acc.commission = acc.commission.Add(c.Amount)
	}
	ids := []uuid.UUID{}
	for key, acc := range rows {
		if key != "" && acc.staff != nil {
			ids = append(ids, acc.staff.ID)
		}
	}
	refs, err := staff.References(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	for key, acc := range rows {
		if key == "" {
			continue
		}
		if ref, ok := refs[acc.staff.ID]; ok {
			acc.staff.Name = ref.Name
		}
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "" {
			return false
		}
		if keys[j] == "" {
			return true
		}
		a, b := rows[keys[i]].staff, rows[keys[j]].staff
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID.String() < b.ID.String()
	})
	total := &staffAccum{}
	for _, key := range keys {
		acc := rows[key]
		out.Rows = append(out.Rows, finishStaffRow(acc))
		total.add(acc)
	}
	out.Totals = finishStaffRow(total)
	return out, nil
}

func addGuest(acc *staffAccum, total, designated, notDesignated, male, female string) error {
	values := []struct {
		dst *int64
		src string
	}{{&acc.guestTotal, total}, {&acc.guestDesignated, designated}, {&acc.guestNot, notDesignated}, {&acc.guestMale, male}, {&acc.guestFemale, female}}
	for _, value := range values {
		n, err := parseShare(value.src)
		if err != nil {
			return err
		}
		*value.dst += n
	}
	return nil
}

func (a *staffAccum) add(other *staffAccum) {
	a.item = a.item.Add(other.item.Change())
	a.consume = a.consume.Add(other.consume.Change())
	a.labor = a.labor.Add(other.labor.Change())
	a.sale = a.sale.Add(other.sale.Change())
	a.cash = a.cash.Add(other.cash.Change())
	a.card = a.card.Add(other.card.Change())
	a.guestTotal += other.guestTotal
	a.guestDesignated += other.guestDesignated
	a.guestNot += other.guestNot
	a.guestMale += other.guestMale
	a.guestFemale += other.guestFemale
	a.serviceItemLines += other.serviceItemLines
	a.commission = a.commission.Add(other.commission.Change())
}

func finishStaffRow(acc *staffAccum) StaffReportRow {
	if acc == nil {
		acc = &staffAccum{}
	}
	return StaffReportRow{
		Staff: acc.staff, ItemPerformance: acc.item.Change(), ConsumePerformance: acc.consume.Change(),
		LaborPerformance: acc.labor.Change(), SalePerformance: acc.sale.Change(),
		CashPerformance: acc.cash.Change(), CardPerformance: acc.card.Change(),
		GuestTotal: formatShare(acc.guestTotal), GuestDesignated: formatShare(acc.guestDesignated),
		GuestNotDesignated: formatShare(acc.guestNot), GuestMale: formatShare(acc.guestMale),
		GuestFemale: formatShare(acc.guestFemale), ServiceItemLines: acc.serviceItemLines,
		CommissionTotal: acc.commission.Change(),
	}
}

// TransformSchema 声明员工引用或空值。
func (StaffReportRow) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["staff"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["staff"], {Type: "null"}}}
	return s
}
