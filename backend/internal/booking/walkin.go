package booking

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// WalkinOccupancy 是一张挂单按项目行给一名员工算出的一段占用。
type WalkinOccupancy struct {
	OrderID    uuid.UUID
	CustomerID uuid.UUID
	StaffID    uuid.UUID
	StartsAt   time.Time
	EndsAt     time.Time
}

// WalkinBookingConflict 是与挂单占用重叠的一条预约或私约占位。
type WalkinBookingConflict struct {
	OrderID       uuid.UUID
	AppointmentID uuid.UUID
	Kind          string
	StaffID       uuid.UUID
	StaffName     string
	StartsAt      time.Time
	EndsAt        time.Time
}

type walkinLine struct {
	OrderID         uuid.UUID
	CustomerID      uuid.UUID
	OpenedAt        time.Time
	LineID          uuid.UUID
	AddedAt         time.Time
	RemovedAt       *time.Time
	ItemKind        string
	DurationMinutes int
	StaffID         *uuid.UUID
}

type walkinGroupKey struct {
	OrderID uuid.UUID
	StaffID uuid.UUID
}

// WalkinOccupancies 返回本店当前营业日挂单按项目行算出的员工占用；staffID 为空时返回全部员工。
func WalkinOccupancies(ctx context.Context, env ops.Env, staffID *uuid.UUID) ([]WalkinOccupancy, error) {
	day, rules, err := settings.CurrentRules(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListWalkinLines(ctx, dbgen.ListWalkinLinesParams{
		StoreID: env.Caller.StoreID, BusinessDate: pgtype.Date{Time: day, Valid: true}, StaffID: staffID,
	})
	if err != nil {
		return nil, err
	}
	lines := make([]walkinLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, walkinLine{
			OrderID: row.OrderID, CustomerID: row.CustomerID, OpenedAt: row.OpenedAt.Time,
			LineID: row.LineID, AddedAt: row.AddedAt.Time, RemovedAt: timePtr(row.RemovedAt),
			ItemKind: row.ItemKind, DurationMinutes: int(row.DurationMinutes), StaffID: row.StaffID,
		})
	}
	return walkinIntervals(lines, rules.BookingDefaultMinutes()), nil
}

// WalkinConflicts 返回给定挂单占用与已有预约、私约占位的冲突，排除同一顾客和关联本单的记录。
func WalkinConflicts(ctx context.Context, env ops.Env, occupancies []WalkinOccupancy) ([]WalkinBookingConflict, error) {
	if len(occupancies) == 0 {
		return []WalkinBookingConflict{}, nil
	}
	staffIDs := make([]uuid.UUID, 0, len(occupancies))
	seenStaff := map[uuid.UUID]bool{}
	from, to := occupancies[0].StartsAt, occupancies[0].EndsAt
	for _, occ := range occupancies {
		if !seenStaff[occ.StaffID] {
			seenStaff[occ.StaffID] = true
			staffIDs = append(staffIDs, occ.StaffID)
		}
		if occ.StartsAt.Before(from) {
			from = occ.StartsAt
		}
		if occ.EndsAt.After(to) {
			to = occ.EndsAt
		}
	}
	rows, err := env.Q.ListWalkinConflictCandidates(ctx, dbgen.ListWalkinConflictCandidatesParams{
		StoreID: env.Caller.StoreID, StaffIds: staffIDs, FromAt: timestamptz(from), ToAt: timestamptz(to),
	})
	if err != nil {
		return nil, err
	}
	type conflictKey struct {
		orderID       uuid.UUID
		appointmentID uuid.UUID
	}
	seen := map[conflictKey]bool{}
	out := []WalkinBookingConflict{}
	for _, occ := range occupancies {
		for _, row := range rows {
			if row.StaffID != occ.StaffID || !row.StartsAt.Time.Before(occ.EndsAt) || !row.EndsAt.Time.After(occ.StartsAt) {
				continue
			}
			if row.CustomerID != nil && *row.CustomerID == occ.CustomerID {
				continue
			}
			if row.OrderID != nil && *row.OrderID == occ.OrderID {
				continue
			}
			key := conflictKey{orderID: occ.OrderID, appointmentID: row.AppointmentID}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, WalkinBookingConflict{
				OrderID: occ.OrderID, AppointmentID: row.AppointmentID, Kind: row.Kind,
				StaffID: row.StaffID, StaffName: row.StaffName, StartsAt: row.StartsAt.Time, EndsAt: row.EndsAt.Time,
			})
		}
	}
	return out, nil
}

// walkinIntervals 把挂单项目行按（单、主做员工）串行排成占用区间；纯函数，便于单测。
func walkinIntervals(lines []walkinLine, defaultMinutes int) []WalkinOccupancy {
	sorted := make([]walkinLine, len(lines))
	copy(sorted, lines)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].OrderID != sorted[j].OrderID {
			return sorted[i].OrderID.String() < sorted[j].OrderID.String()
		}
		if !sorted[i].AddedAt.Equal(sorted[j].AddedAt) {
			return sorted[i].AddedAt.Before(sorted[j].AddedAt)
		}
		return sorted[i].LineID.String() < sorted[j].LineID.String()
	})
	groups := map[walkinGroupKey][]walkinLine{}
	var order []walkinGroupKey
	for _, line := range sorted {
		if line.RemovedAt != nil || line.ItemKind != "service" || line.StaffID == nil {
			continue
		}
		key := walkinGroupKey{OrderID: line.OrderID, StaffID: *line.StaffID}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], line)
	}
	out := []WalkinOccupancy{}
	for _, key := range order {
		group := groups[key]
		cursor := group[0].OpenedAt
		firstStart := time.Time{}
		total := 0
		current := -1
		for _, line := range group {
			start := cursor
			if line.AddedAt.After(start) {
				start = line.AddedAt
			}
			if firstStart.IsZero() {
				firstStart = start
			}
			end := start.Add(time.Duration(line.DurationMinutes) * time.Minute)
			cursor = end
			total += line.DurationMinutes
			if line.DurationMinutes <= 0 {
				continue
			}
			if current >= 0 && !start.After(out[current].EndsAt) {
				out[current].EndsAt = end
				continue
			}
			out = append(out, WalkinOccupancy{
				OrderID: key.OrderID, CustomerID: line.CustomerID, StaffID: key.StaffID, StartsAt: start, EndsAt: end,
			})
			current = len(out) - 1
		}
		if total == 0 {
			out = append(out, WalkinOccupancy{
				OrderID: key.OrderID, CustomerID: group[0].CustomerID, StaffID: key.StaffID,
				StartsAt: firstStart, EndsAt: firstStart.Add(time.Duration(defaultMinutes) * time.Minute),
			})
		}
	}
	return out
}
