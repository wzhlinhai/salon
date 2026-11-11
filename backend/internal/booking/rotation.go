package booking

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// OccupiedStaff 一次查询返回给定员工中在 at 时刻被预约或私约占位占用的集合。
// 轮牌分配用它做单次批量判断，不按候选员工逐人查询。
func OccupiedStaff(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, staffIDs []uuid.UUID, at time.Time) (map[uuid.UUID]bool, error) {
	if len(staffIDs) == 0 {
		return map[uuid.UUID]bool{}, nil
	}
	rows, err := q.OccupiedStaff(ctx, dbgen.OccupiedStaffParams{StoreID: storeID, StaffIds: staffIDs, At: timestamptz(at)})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(rows))
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// OrderHasCompletedAppointment 报告一张单是否关联了已完成的预约，供轮牌“按预约”记半轮。
func OrderHasCompletedAppointment(ctx context.Context, q *dbgen.Queries, storeID, orderID uuid.UUID) (bool, error) {
	return q.OrderHasCompletedAppointment(ctx, dbgen.OrderHasCompletedAppointmentParams{StoreID: storeID, OrderID: orderID})
}
