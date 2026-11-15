package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/attendance"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

const (
	// SelfBookingSlotMinutes 是顾客自助预约的开始时间网格：整点或半点。
	SelfBookingSlotMinutes = 30
	// SelfBookingLimit 是每位顾客在本店同时保留的待到店自助预约上限。
	SelfBookingLimit = 3
)

// SelfBookingStaff 是顾客可预约的发型师。
type SelfBookingStaff struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	LevelName *string   `json:"level_name" nullable:"true"`
}

// SelfBookingItem 是顾客可预约的项目，不含价格。
type SelfBookingItem struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	DurationMinutes int       `json:"duration_minutes"`
}

// SelfBookingRuleSet 是顾客预约需要遵守的门店规则。
type SelfBookingRuleSet struct {
	BusinessHours   settings.TimeRange `json:"business_hours"`
	MinLeadMinutes  int                `json:"min_lead_minutes"`
	MaxDaysAhead    int                `json:"max_days_ahead"`
	SlotMinutes     int                `json:"slot_minutes"`
	DefaultDuration int                `json:"default_duration_minutes"`
}

// SelfBookableStaff 返回本店在职且允许顾客自助预约的发型师。
func SelfBookableStaff(ctx context.Context, env ops.Env) ([]SelfBookingStaff, error) {
	rows, err := env.Q.ListSelfBookableStaff(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := make([]SelfBookingStaff, 0, len(rows))
	for _, row := range rows {
		out = append(out, SelfBookingStaff{ID: row.ID, Name: row.Name, LevelName: row.LevelName})
	}
	return out, nil
}

// SelfBookableItems 返回本店启用且可预约的项目。
func SelfBookableItems(ctx context.Context, env ops.Env) ([]SelfBookingItem, error) {
	rows, err := env.Q.ListBookableItems(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := make([]SelfBookingItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, SelfBookingItem{ID: row.ID, Name: row.Name, DurationMinutes: int(row.DurationMinutes)})
	}
	return out, nil
}

// SelfBookingRules 返回今天执行的顾客预约规则。
func SelfBookingRules(ctx context.Context, env ops.Env) (SelfBookingRuleSet, error) {
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return SelfBookingRuleSet{}, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return SelfBookingRuleSet{}, err
	}
	return selfRuleSet(rules), nil
}

func selfRuleSet(rules settings.Rules) SelfBookingRuleSet {
	return SelfBookingRuleSet{
		BusinessHours:   rules.BusinessHours(),
		MinLeadMinutes:  rules.BookingMinLeadMinutes(),
		MaxDaysAhead:    rules.BookingMaxDaysAhead(),
		SlotMinutes:     SelfBookingSlotMinutes,
		DefaultDuration: rules.BookingDefaultMinutes(),
	}
}

type occupiedInterval struct {
	Start time.Time
	End   time.Time
}

type selfBookingWindow struct {
	rules     settings.Rules
	now       time.Time
	today     time.Time
	day       time.Time
	dayStart  time.Time
	dayEnd    time.Time
	shiftFrom *time.Time
	shiftTo   *time.Time
	occupied  []occupiedInterval
}

// SelfBookingSlots 返回某发型师在某个营业日按 30 分钟网格开始的可用时间。
func SelfBookingSlots(ctx context.Context, env ops.Env, customerID, staffID uuid.UUID, day time.Time, itemIDs []uuid.UUID) ([]string, error) {
	if _, err := selfBookableStaff(ctx, env, staffID, false); err != nil {
		return nil, err
	}
	_, total, err := selfItems(ctx, env, itemIDs, false)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	window, err := loadSelfBookingWindow(ctx, env, day, staffID, rules)
	if err != nil {
		return nil, err
	}
	count, err := env.Q.CountCustomerSelfBookings(ctx, dbgen.CountCustomerSelfBookingsParams{
		StoreID: env.Caller.StoreID, CustomerID: &customerID, Now: timestamptz(env.Now),
	})
	if err != nil {
		return nil, err
	}
	if count >= SelfBookingLimit {
		return []string{}, nil
	}
	duration := rules.BookingDefaultMinutes()
	if total > 0 {
		duration = total
	}
	startOfDay, err := localDayStart(day)
	if err != nil {
		return nil, err
	}
	open, err := localClock(startOfDay, rules.BusinessHours().Open)
	if err != nil {
		return nil, err
	}
	close, err := localClock(startOfDay, rules.BusinessHours().Close)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for startsAt := ceilSlot(open); !startsAt.Add(time.Duration(duration) * time.Minute).After(close); startsAt = startsAt.Add(SelfBookingSlotMinutes * time.Minute) {
		if startsAt.Before(window.dayStart) || !startsAt.Before(window.dayEnd) {
			continue
		}
		if err := evaluateSelfSlot(window, startsAt, duration); err == nil {
			out = append(out, startsAt.Format("15:04"))
		}
	}
	return out, nil
}

// CreateSelfAppointment 创建顾客本人的待确认预约，并写事件和审计。
func CreateSelfAppointment(ctx context.Context, env ops.Env, customerID, staffID uuid.UUID, startsAt time.Time, itemIDs []uuid.UUID, note *string) (*Appointment, error) {
	customer, err := customers.LockSurvivingSelf(ctx, env, customerID)
	if err != nil {
		return nil, err
	}
	customerID = customer.ID
	if _, err := selfBookableStaff(ctx, env, staffID, true); err != nil {
		if errors.Is(err, ops.ErrNotFound) {
			return nil, ops.Rule("该发型师不可预约")
		}
		return nil, err
	}
	items, total, err := selfItems(ctx, env, itemIDs, true)
	if err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, startsAt)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	window, err := loadSelfBookingWindow(ctx, env, day, staffID, rules)
	if err != nil {
		return nil, err
	}
	duration := rules.BookingDefaultMinutes()
	if total > 0 {
		duration = total
	}
	if err := evaluateSelfSlot(window, startsAt, duration); err != nil {
		return nil, err
	}
	if err := env.Q.LockCustomerSelfBooking(ctx, dbgen.LockCustomerSelfBookingParams{StoreID: env.Caller.StoreID, CustomerID: customerID}); err != nil {
		return nil, err
	}
	count, err := env.Q.CountCustomerSelfBookings(ctx, dbgen.CountCustomerSelfBookingsParams{
		StoreID: env.Caller.StoreID, CustomerID: &customerID, Now: timestamptz(env.Now),
	})
	if err != nil {
		return nil, err
	}
	if count >= SelfBookingLimit {
		return nil, ops.Rule("最多同时保留 3 个待到店的自助预约")
	}
	endsAt := startsAt.Add(time.Duration(duration) * time.Minute)
	row, err := env.Q.CreateAppointment(ctx, dbgen.CreateAppointmentParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: staffID, CustomerID: &customerID,
		Kind: KindAppointment, StartsAt: timestamptz(startsAt), EndsAt: timestamptz(endsAt), Status: StatusPending,
		Note: trimNote(note), CreatedByCustomer: &customerID,
	})
	if err != nil {
		if isExclusionViolation(err) {
			return nil, conflictRule(nil)
		}
		return nil, err
	}
	for i, item := range items {
		if err := env.Q.AddAppointmentItem(ctx, dbgen.AddAppointmentItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AppointmentID: row.ID, ItemID: item.ID, Position: int32(i + 1)}); err != nil {
			return nil, err
		}
	}
	if err := addEvent(ctx, env, row.ID, "create", nil, nil, row.StartsAt.Time, row.EndsAt.Time, nil, row.StaffID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "book_appointment", target(row), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// CancelSelfAppointment 取消顾客本人尚未开始的待确认或已确认预约。
func CancelSelfAppointment(ctx context.Context, env ops.Env, customerID, id uuid.UUID) (*Appointment, error) {
	customer, err := customers.LockSurvivingSelf(ctx, env, customerID)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.LockAppointment(ctx, dbgen.LockAppointmentParams{StoreID: env.Caller.StoreID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.Kind != KindAppointment || row.CustomerID == nil || *row.CustomerID != customer.ID {
		return nil, ops.ErrNotFound
	}
	if row.Status != StatusPending && row.Status != StatusConfirmed {
		return nil, ops.Rule("只有待确认或已确认的预约可以取消")
	}
	if !row.StartsAt.Time.After(env.Now) {
		return nil, ops.Rule("已经开始的预约不能取消")
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.SetAppointmentStatus(ctx, dbgen.SetAppointmentStatusParams{StoreID: env.Caller.StoreID, ID: row.ID, Status: StatusCancelled})
	if err != nil {
		return nil, err
	}
	reason := CancelReasonCustomer
	if err := addEvent(ctx, env, row.ID, "cancel", nil, nil, before.StartsAt, before.EndsAt, nil, before.Staff.ID, &reason, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "cancel_my_appointment", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func selfBookableStaff(ctx context.Context, env ops.Env, staffID uuid.UUID, lock bool) (dbgen.Staff, error) {
	var row dbgen.Staff
	var err error
	if lock {
		row, err = env.Q.LockSelfBookableStaff(ctx, dbgen.LockSelfBookableStaffParams{StoreID: env.Caller.StoreID, ID: staffID})
	} else {
		row, err = env.Q.GetSelfBookableStaff(ctx, dbgen.GetSelfBookableStaffParams{StoreID: env.Caller.StoreID, ID: staffID})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ops.ErrNotFound
	}
	return row, err
}

func selfItems(ctx context.Context, env ops.Env, ids []uuid.UUID, lock bool) ([]dbgen.Item, int, error) {
	if len(ids) == 0 {
		return nil, 0, ops.Rule("请至少选择一个项目")
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, 0, ops.Rule("项目不能重复")
		}
		seen[id] = true
	}
	rows := map[uuid.UUID]dbgen.Item{}
	if lock {
		locked, err := catalog.LockItems(ctx, env.Q, env.Caller.StoreID, ids...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ops.Rule("项目不存在")
		}
		if err != nil {
			return nil, 0, err
		}
		rows = locked
	} else {
		list, err := env.Q.GetItems(ctx, dbgen.GetItemsParams{StoreID: env.Caller.StoreID, Ids: ids})
		if err != nil {
			return nil, 0, err
		}
		for _, row := range list {
			rows[row.ID] = row
		}
	}
	return validateSelectedItems(ids, rows)
}

func validateSelectedItems(ids []uuid.UUID, rows map[uuid.UUID]dbgen.Item) ([]dbgen.Item, int, error) {
	items := make([]dbgen.Item, 0, len(ids))
	total := 0
	for _, id := range ids {
		item, ok := rows[id]
		if !ok {
			return nil, 0, ops.Rule("项目不存在")
		}
		if !item.Active {
			return nil, 0, ops.Rule("项目已停用")
		}
		if !item.Bookable {
			return nil, 0, ops.Rule("该项目不可预约")
		}
		items = append(items, item)
		total += int(item.DurationMinutes)
	}
	return items, total, nil
}

func loadSelfBookingWindow(ctx context.Context, env ops.Env, day time.Time, staffID uuid.UUID, rules settings.Rules) (selfBookingWindow, error) {
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return selfBookingWindow{}, err
	}
	timelines, err := attendance.StaffShiftTimelines(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return selfBookingWindow{}, err
	}
	dayStart, dayEnd, err := businessDayBounds(day, rules)
	if err != nil {
		return selfBookingWindow{}, err
	}
	window := selfBookingWindow{rules: rules, now: env.Now, today: today, day: day, dayStart: dayStart, dayEnd: dayEnd}
	if change, ok := timelines[staffID].At(day); ok && change.Value.Shift != nil {
		date := day.Format(settings.DateLayout)
		from, err := localClockDate(date, change.Value.Shift.StartTime)
		if err != nil {
			return selfBookingWindow{}, err
		}
		to, err := localClockDate(date, change.Value.Shift.EndTime)
		if err != nil {
			return selfBookingWindow{}, err
		}
		window.shiftFrom, window.shiftTo = &from, &to
	}
	rows, err := env.Q.ListStaffOccupied(ctx, dbgen.ListStaffOccupiedParams{
		StoreID: env.Caller.StoreID, StaffID: staffID, DayStart: timestamptz(dayStart), DayEnd: timestamptz(dayEnd),
	})
	if err != nil {
		return selfBookingWindow{}, err
	}
	window.occupied = make([]occupiedInterval, 0, len(rows))
	for _, row := range rows {
		window.occupied = append(window.occupied, occupiedInterval{Start: row.StartsAt.Time, End: row.EndsAt.Time})
	}
	return window, nil
}

func evaluateSelfSlot(window selfBookingWindow, startsAt time.Time, duration int) error {
	local := startsAt.In(settings.Location)
	if local.Minute()%SelfBookingSlotMinutes != 0 || local.Second() != 0 || local.Nanosecond() != 0 {
		return ops.Rule("预约开始时间要在整点或半点")
	}
	if err := CheckLeadTime(window.rules.BookingMinLeadMinutes(), window.now, startsAt); err != nil {
		return err
	}
	endsAt := startsAt.Add(time.Duration(duration) * time.Minute)
	if err := checkSelfSchedule(window, startsAt, endsAt); err != nil {
		return err
	}
	if window.shiftFrom != nil && (startsAt.Before(*window.shiftFrom) || endsAt.After(*window.shiftTo)) {
		return ops.Rule("该时段发型师不上班")
	}
	for _, occupied := range window.occupied {
		if startsAt.Before(occupied.End) && endsAt.After(occupied.Start) {
			return conflictRule(nil)
		}
	}
	return nil
}

func checkSelfSchedule(window selfBookingWindow, startsAt, endsAt time.Time) error {
	if !endsAt.After(startsAt) {
		return ops.Rule("结束时间要晚于开始时间")
	}
	date := window.day.Format(settings.DateLayout)
	hours := window.rules.BusinessHours()
	open, err := localClockDate(date, hours.Open)
	if err != nil {
		return err
	}
	close, err := localClockDate(date, hours.Close)
	if err != nil {
		return err
	}
	if startsAt.Before(open) || endsAt.After(close) {
		return ops.Rule("预约时间要在营业时段（" + hours.Open + "–" + hours.Close + "）内")
	}
	if window.day.Before(window.today) {
		return ops.Rule("预约日期不能早于今天")
	}
	max := window.rules.BookingMaxDaysAhead()
	if window.day.After(window.today.AddDate(0, 0, max)) {
		return ops.Rule(fmt.Sprintf("最多只能预约 %d 天内的时段", max))
	}
	if startsAt.Before(window.now.Add(-5 * time.Minute)) {
		return ops.Rule("不能预约已经过去的时段")
	}
	return nil
}

func businessDayBounds(day time.Time, rules settings.Rules) (time.Time, time.Time, error) {
	start, err := localClockDate(day.Format(settings.DateLayout), rules.BusinessDayEnd())
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, start.AddDate(0, 0, 1), nil
}

func localDayStart(day time.Time) (time.Time, error) {
	return localClockDate(day.Format(settings.DateLayout), "00:00")
}

func localClock(start time.Time, clock string) (time.Time, error) {
	return localClockDate(start.Format(settings.DateLayout), clock)
}

func localClockDate(date, clock string) (time.Time, error) {
	at, err := time.ParseInLocation(settings.DateLayout+" 15:04", date+" "+clock, settings.Location)
	if err != nil {
		return time.Time{}, fmt.Errorf("本地时间格式不正确（%s %s）: %w", date, clock, err)
	}
	return at, nil
}

func ceilSlot(t time.Time) time.Time {
	if t.Minute()%SelfBookingSlotMinutes == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
		return t
	}
	return t.Truncate(time.Minute).Add(time.Duration(SelfBookingSlotMinutes-t.Minute()%SelfBookingSlotMinutes) * time.Minute)
}
