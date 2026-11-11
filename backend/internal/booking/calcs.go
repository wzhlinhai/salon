package booking

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// 预约状态只能沿这张表前进；任何表外的变化都拒绝。
var statusTransitions = map[string]map[string]bool{
	StatusPending:   {StatusConfirmed: true, StatusCancelled: true},
	StatusConfirmed: {StatusCompleted: true, StatusCancelled: true, StatusNoShow: true},
}

func requireTransition(row dbgen.Appointment, to string) error {
	if statusTransitions[row.Status][to] {
		return nil
	}
	return ops.Rule("预约状态不能从 " + row.Status + " 变更为 " + to)
}

// CheckLeadTime 检查顾客自助预约是否满足提前量；前台代约不调用它，P0-19 自助预约复用。
func CheckLeadTime(requiredMinutes int, now, startsAt time.Time) error {
	if requiredMinutes <= 0 {
		return nil
	}
	if startsAt.Before(now.Add(time.Duration(requiredMinutes) * time.Minute)) {
		return ops.Rule(fmt.Sprintf("预约至少要提前 %d 分钟", requiredMinutes))
	}
	return nil
}

// checkSchedule 检查预约是否落在营业时段、可约天数和今天之内。
func checkSchedule(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, rules settings.Rules, now, startsAt, endsAt time.Time) error {
	if !endsAt.After(startsAt) {
		return ops.Rule("结束时间要晚于开始时间")
	}
	startLocal, endLocal := startsAt.In(settings.Location), endsAt.In(settings.Location)
	if startLocal.Format(settings.DateLayout) != endLocal.Format(settings.DateLayout) {
		return ops.Rule("预约不能跨天")
	}
	hours := rules.BusinessHours()
	startClock, endClock := startLocal.Format("15:04"), endLocal.Format("15:04")
	if startClock < hours.Open || endClock > hours.Close {
		return ops.Rule("预约时间要在营业时段（" + hours.Open + "–" + hours.Close + "）内")
	}
	today, err := settings.BusinessDate(ctx, q, storeID, now)
	if err != nil {
		return err
	}
	day := settings.Date(startsAt)
	if day.Before(today) {
		return ops.Rule("预约日期不能早于今天")
	}
	max := rules.BookingMaxDaysAhead()
	if day.After(today.AddDate(0, 0, max)) {
		return ops.Rule(fmt.Sprintf("最多只能预约 %d 天内的时段", max))
	}
	if startsAt.Before(now.Add(-5 * time.Minute)) {
		return ops.Rule("不能预约已经过去的时段")
	}
	return nil
}

// normalizeCancelNote 校验取消原因，返回 trim 后的说明。
func normalizeCancelNote(reason string, note *string) (*string, error) {
	if reason == CancelReasonOther && (note == nil || strings.TrimSpace(*note) == "") {
		return nil, ops.Rule("取消原因为其他时要填写说明")
	}
	if note == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*note)
	if trimmed == "" {
		return nil, nil
	}
	return &trimmed, nil
}

func trimNote(note *string) *string {
	if note == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*note)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func conflictRule(row *dbgen.Appointment) error {
	if row == nil {
		return ops.Rule("该时段已有预约或占位")
	}
	return ops.Rule("该时段已有预约或占位：" + row.ID.String())
}

func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23P01"
}
