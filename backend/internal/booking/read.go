package booking

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// OccupiedStaff 和 OrderHasCompletedAppointment 的轮牌接口在同文件的 rotation.go。

func inStaffScope(env ops.Env, staffID uuid.UUID) bool {
	if env.Grant.Scope != ops.ScopeSelf {
		return true
	}
	return env.Caller.StaffID == staffID
}

func requireStaffScope(env ops.Env, staffID uuid.UUID) error {
	if inStaffScope(env, staffID) {
		return nil
	}
	return ops.ErrNotFound
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func timestamptz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func loadAppointments(ctx context.Context, env ops.Env, rows []dbgen.Appointment, withEvents bool) ([]Appointment, error) {
	if len(rows) == 0 {
		return []Appointment{}, nil
	}
	staffIDs := make([]uuid.UUID, 0, len(rows)*2)
	customerIDs := make([]uuid.UUID, 0, len(rows))
	appointmentIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		staffIDs = append(staffIDs, row.StaffID, row.CreatedBy)
		if row.CustomerID != nil {
			customerIDs = append(customerIDs, *row.CustomerID)
		}
		appointmentIDs = append(appointmentIDs, row.ID)
	}
	refs, err := env.Q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: env.Caller.StoreID, Ids: staffIDs})
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]staff.Ref, len(refs))
	for _, row := range refs {
		names[row.ID] = staff.Ref{ID: row.ID, Name: row.Name}
	}
	customersByID := map[uuid.UUID]dbgen.Customer{}
	if len(customerIDs) > 0 {
		cr, err := env.Q.GetCustomers(ctx, dbgen.GetCustomersParams{StoreID: env.Caller.StoreID, Ids: customerIDs})
		if err != nil {
			return nil, err
		}
		for _, row := range cr {
			customersByID[row.ID] = row
		}
	}
	itemRows, err := env.Q.ListAppointmentItems(ctx, dbgen.ListAppointmentItemsParams{StoreID: env.Caller.StoreID, AppointmentIds: appointmentIDs})
	if err != nil {
		return nil, err
	}
	items := map[uuid.UUID][]AppointmentItem{}
	for _, row := range itemRows {
		items[row.AppointmentID] = append(items[row.AppointmentID], AppointmentItem{ItemID: row.ItemID, Name: row.Name, Position: int(row.Position), DurationMinutes: int(row.DurationMinutes)})
	}
	events := map[uuid.UUID][]AppointmentEvent{}
	if withEvents {
		eventRows, err := env.Q.ListAppointmentEvents(ctx, dbgen.ListAppointmentEventsParams{StoreID: env.Caller.StoreID, AppointmentIds: appointmentIDs})
		if err != nil {
			return nil, err
		}
		for _, row := range eventRows {
			event := AppointmentEvent{
				ID: row.ID, Kind: row.Kind, FromStartsAt: timePtr(row.FromStartsAt), FromEndsAt: timePtr(row.FromEndsAt),
				ToStartsAt: row.ToStartsAt.Time, ToEndsAt: row.ToEndsAt.Time, Reason: row.Reason, Note: row.Note,
				Actor: staff.Ref{ID: row.ActorStaffID, Name: row.ActorName}, CreatedAt: row.CreatedAt.Time,
			}
			if row.FromStaffID != nil {
				name := ""
				if row.FromStaffName != nil {
					name = *row.FromStaffName
				}
				event.FromStaff = &staff.Ref{ID: *row.FromStaffID, Name: name}
			}
			event.ToStaff = staff.Ref{ID: row.ToStaffID, Name: row.ToStaffName}
			events[row.AppointmentID] = append(events[row.AppointmentID], event)
		}
	}
	out := make([]Appointment, 0, len(rows))
	for _, row := range rows {
		view := Appointment{
			ID: row.ID, Kind: row.Kind, Status: row.Status, StartsAt: row.StartsAt.Time, EndsAt: row.EndsAt.Time,
			Note: row.Note, OrderID: row.OrderID, Staff: names[row.StaffID], CreatedBy: names[row.CreatedBy],
			CreatedAt: row.CreatedAt.Time, Items: items[row.ID], Events: events[row.ID],
		}
		if view.Items == nil {
			view.Items = []AppointmentItem{}
		}
		if view.Events == nil {
			view.Events = []AppointmentEvent{}
		}
		if row.CustomerID != nil {
			view.Customer = customerRef(env, *row.CustomerID, customersByID[*row.CustomerID])
		}
		out = append(out, view)
	}
	return out, nil
}

func customerRef(env ops.Env, id uuid.UUID, row dbgen.Customer) *AppointmentCustomer {
	ref := &AppointmentCustomer{ID: id}
	if !customers.CanView(env.Caller, row) {
		return ref
	}
	ref.Name = row.Name
	if row.Phone != nil {
		phone := *row.Phone
		if !env.Caller.Has(perm.CustomerFullPhone) {
			phone = privacy.MaskPhone(phone)
			ref.PhoneMasked = true
		}
		ref.Phone = &phone
	}
	return ref
}

func getAppointment(ctx context.Context, env ops.Env, in *GetAppointmentInput) (*Appointment, error) {
	row, err := env.Q.GetAppointment(ctx, dbgen.GetAppointmentParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !inStaffScope(env, row.StaffID) {
		return nil, ops.ErrNotFound
	}
	views, err := loadAppointments(ctx, env, []dbgen.Appointment{row}, true)
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

func listAppointments(ctx context.Context, env ops.Env, in *ListAppointmentsInput) (*AppointmentList, error) {
	q, err := daterange.Parse(env.Caller, perm.BookingManage, in.From, in.To, in.StaffID, 0)
	if err != nil {
		var rule *ops.RuleError
		if errors.As(err, &rule) {
			return nil, &ops.ToolError{Messages: []string{rule.Message}}
		}
		return nil, err
	}
	out := &AppointmentList{Entries: []Appointment{}}
	if q.Empty {
		return out, nil
	}
	from := time.Date(q.From.Year(), q.From.Month(), q.From.Day(), 0, 0, 0, 0, settings.Location)
	to := time.Date(q.To.Year(), q.To.Month(), q.To.Day(), 0, 0, 0, 0, settings.Location).AddDate(0, 0, 1)
	rows, err := env.Q.ListAppointments(ctx, dbgen.ListAppointmentsParams{StoreID: env.Caller.StoreID, FromAt: timestamptz(from), ToAt: timestamptz(to), StaffID: q.StaffID})
	if err != nil {
		return nil, err
	}
	views, err := loadAppointments(ctx, env, rows, false)
	if err != nil {
		return nil, err
	}
	out.Entries = views
	return out, nil
}

// ListForCustomer 读取顾客本人的预约：待确认和已确认的在未来先列，其余最近 90 天按时间倒序。
func ListForCustomer(ctx context.Context, env ops.Env, customerID uuid.UUID, now time.Time, limit int32) ([]Appointment, error) {
	if limit == 0 {
		limit = 100
	}
	rows, err := env.Q.ListCustomerAppointments(ctx, dbgen.ListCustomerAppointmentsParams{
		StoreID: env.Caller.StoreID, CustomerID: &customerID, Now: timestamptz(now), RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	return loadAppointments(ctx, env, rows, false)
}
