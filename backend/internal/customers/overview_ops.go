package customers

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// 顾客概览里"该回来了"和"沉睡"的分界营业日数。
const customerOverviewStaleDays = 90

// 新客统计的营业日窗口天数，含今天。
const customerOverviewNewDays = 30

// CustomerOverviewInput 是顾客概览列表的筛选、排序和翻页参数。
type CustomerOverviewInput struct {
	Query              string     `json:"query,omitempty"`
	ResponsibleStaffID *uuid.UUID `json:"responsible_staff_id,omitempty"`
	Segment            string     `json:"segment,omitempty" enum:"all,today,due,balance,new,sleeping" doc:"分组：all 全部，today 今天要来，due 该回来了，balance 卡里有余额，new 30 天新客，sleeping 沉睡"`
	Sort               string     `json:"sort,omitempty" enum:"last_visit,overdue,spend,created" doc:"排序：last_visit 最近到店，overdue 超期天数，spend 累计消费，created 建档时间"`
	Limit              int32      `json:"limit,omitempty" minimum:"0" maximum:"100"`
	Offset             int32      `json:"offset,omitempty" minimum:"0"`
}

// CustomerSegmentsInput 是顾客分组统计的筛选参数。
type CustomerSegmentsInput struct {
	ResponsibleStaffID *uuid.UUID `json:"responsible_staff_id,omitempty"`
}

// CustomerNextAppointment 是顾客下一次待到店的预约。
type CustomerNextAppointment struct {
	ID       uuid.UUID `json:"id"`
	StartsAt time.Time `json:"starts_at"`
	Staff    staff.Ref `json:"staff"`
}

// CustomerOverview 是顾客概览列表的一行。
type CustomerOverview struct {
	Customer           Customer                 `json:"customer"`
	VisitCount         int32                    `json:"visit_count" doc:"客次"`
	FirstVisitDate     *string                  `json:"first_visit_date" format:"date" doc:"第一次结账的营业日"`
	LastVisitDate      *string                  `json:"last_visit_date" format:"date" doc:"最后一次结账的营业日"`
	DaysSinceLastVisit *int32                   `json:"days_since_last_visit" doc:"距最后一次到店的营业日数"`
	CycleDays          *int32                   `json:"cycle_days" doc:"到店间隔：首末到店营业日之间平均多少天，至少两个不同营业日才有"`
	OverdueDays        *int32                   `json:"overdue_days" doc:"超过到店间隔的天数，没有超过为空"`
	LastVisitItems     []string                 `json:"last_visit_items" doc:"最后一次到店的项目名，按项目行添加顺序"`
	LastVisitStaff     []staff.Ref              `json:"last_visit_staff" doc:"最后一次到店的员工，按岗位顺序"`
	TotalSpend         money.Amount             `json:"total_spend" doc:"累计实收，不扣售后退款"`
	StoredBalance      money.Amount             `json:"stored_balance" doc:"本店发行、未退未过期的卡当前本金加赠送金"`
	CountRemaining     int64                    `json:"count_remaining" doc:"上述卡剩余购买次数加赠送次数"`
	NextAppointment    *CustomerNextAppointment `json:"next_appointment"`
}

// TransformSchema 声明下一次预约可以是空值。
func (CustomerOverview) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["next_appointment"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["next_appointment"], {Type: "null"}}}
	return s
}

// CustomerOverviewList 是顾客概览列表和匹配全部筛选条件的总行数。
type CustomerOverviewList struct {
	Items []CustomerOverview `json:"items"`
	Total int                `json:"total"`
}

// CustomerSegmentAll 是全部顾客和其中本月到店过的顾客数。
type CustomerSegmentAll struct {
	Count            int64 `json:"count"`
	VisitedThisMonth int64 `json:"visited_this_month"`
}

// CustomerSegmentToday 是今天要来的人数和其中已经到店的人数。
type CustomerSegmentToday struct {
	Count   int64 `json:"count"`
	Arrived int64 `json:"arrived"`
}

// CustomerSegmentDue 是该回来的人数和其中有卡余额的人数。
type CustomerSegmentDue struct {
	Count       int64 `json:"count"`
	WithBalance int64 `json:"with_balance"`
}

// CustomerSegmentBalance 是卡里有余额的人数和储值余额合计。
type CustomerSegmentBalance struct {
	Count       int64        `json:"count"`
	StoredTotal money.Amount `json:"stored_total"`
}

// CustomerSegmentNew 是 30 天新客人数和其中只来过一次的人数。
type CustomerSegmentNew struct {
	Count       int64 `json:"count"`
	SingleVisit int64 `json:"single_visit"`
}

// CustomerSegmentSleeping 是沉睡顾客人数。
type CustomerSegmentSleeping struct {
	Count int64 `json:"count"`
}

// CustomerSegments 是顾客概览的六个分组数量。
type CustomerSegments struct {
	All      CustomerSegmentAll      `json:"all"`
	Today    CustomerSegmentToday    `json:"today"`
	Due      CustomerSegmentDue      `json:"due"`
	Balance  CustomerSegmentBalance  `json:"balance"`
	New      CustomerSegmentNew      `json:"new"`
	Sleeping CustomerSegmentSleeping `json:"sleeping"`
}

func registerOverview(r *ops.Registry) {
	ops.Register(r, ops.Operation[CustomerOverviewInput, CustomerOverviewList]{
		Name: "list_customer_overview", Summary: "按分组查看顾客的到店、消费、卡余额和下一次预约", Kind: ops.Read,
		Permission: perm.CustomerView, AI: ops.AIDirect,
		Handle: listCustomerOverview,
	})
	ops.Register(r, ops.Operation[CustomerSegmentsInput, CustomerSegments]{
		Name: "get_customer_segments", Summary: "统计顾客概览六个分组的人数和提示数字", Kind: ops.Read,
		Permission: perm.CustomerView, AI: ops.AIDirect,
		Handle: getCustomerSegments,
	})
}

// overviewWindow 返回今天的营业日和预约日历用的当天起止时刻。
func overviewWindow(ctx context.Context, env ops.Env) (time.Time, time.Time, time.Time, error) {
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, err
	}
	// 与预约列表的营业日视图一致：营业日的 00:00 到次日 00:00。
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, settings.Location)
	return today, start, start.AddDate(0, 0, 1), nil
}

func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func moneyFromText(value string) (money.Amount, error) {
	var out money.Amount
	err := out.Scan(value)
	return out, err
}

func listCustomerOverview(ctx context.Context, env ops.Env, in *CustomerOverviewInput) (*CustomerOverviewList, error) {
	segment := in.Segment
	if segment == "" {
		segment = "all"
	}
	sort := in.Sort
	if sort == "" {
		sort = "last_visit"
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	self, err := viewScope(env.Caller)
	if err != nil {
		return nil, err
	}
	today, start, end, err := overviewWindow(ctx, env)
	if err != nil {
		return nil, err
	}
	params := dbgen.ListCustomerOverviewParams{
		Sort: sort, RowOffset: in.Offset, RowLimit: limit, StoreID: env.Caller.StoreID,
		ResponsibleStaffID: in.ResponsibleStaffID, SelfID: self, Query: in.Query,
		Today: pgDate(today), Now: pgTime(env.Now), Segment: segment,
		TodayStart: pgTime(start), TodayEnd: pgTime(end),
		StaleDays: customerOverviewStaleDays, NewDays: customerOverviewNewDays,
	}
	rows, err := env.Q.ListCustomerOverview(ctx, params)
	if err != nil {
		return nil, err
	}
	out := &CustomerOverviewList{Items: []CustomerOverview{}}
	if len(rows) == 0 {
		// 翻过最后一页时窗口里的 count(*) 不会返回；用第一页取同一次筛选的总数。
		if in.Offset > 0 {
			params.RowOffset, params.RowLimit = 0, 1
			probe, err := env.Q.ListCustomerOverview(ctx, params)
			if err != nil {
				return nil, err
			}
			if len(probe) > 0 {
				out.Total = int(probe[0].Total)
			}
		}
		return out, nil
	}
	out.Total = int(rows[0].Total)
	baseRows := make([]dbgen.Customer, len(rows))
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		baseRows[i] = dbgen.Customer{
			ID: row.ID, TenantID: row.TenantID, StoreID: row.StoreID, Name: row.Name, Phone: row.Phone,
			Gender: row.Gender, SourceID: row.SourceID, ResponsibleStaffID: row.ResponsibleStaffID,
			Note: row.Note, HairTexture: row.HairTexture, Preferences: row.Preferences, Dislikes: row.Dislikes,
			MergedInto: row.MergedInto, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
			CreatedByCustomer: row.CreatedByCustomer,
		}
		ids[i] = row.ID
	}
	views, err := views(ctx, env, baseRows)
	if err != nil {
		return nil, err
	}
	itemRows, err := env.Q.ListCustomerOverviewVisitItems(ctx, dbgen.ListCustomerOverviewVisitItemsParams{StoreID: env.Caller.StoreID, CustomerIds: ids})
	if err != nil {
		return nil, err
	}
	items := map[uuid.UUID][]string{}
	for _, row := range itemRows {
		items[row.CustomerID] = append(items[row.CustomerID], row.ItemName)
	}
	staffRows, err := env.Q.ListCustomerOverviewVisitStaff(ctx, dbgen.ListCustomerOverviewVisitStaffParams{StoreID: env.Caller.StoreID, CustomerIds: ids})
	if err != nil {
		return nil, err
	}
	lastStaff := map[uuid.UUID][]staff.Ref{}
	for _, row := range staffRows {
		lastStaff[row.CustomerID] = append(lastStaff[row.CustomerID], staff.Ref{ID: row.StaffID, Name: row.Name})
	}
	appointmentIDs := []uuid.UUID{}
	for _, row := range rows {
		if row.NextAppointmentID != nil {
			appointmentIDs = append(appointmentIDs, *row.NextAppointmentID)
		}
	}
	appointments := map[uuid.UUID]CustomerNextAppointment{}
	if len(appointmentIDs) > 0 {
		appointmentRows, err := env.Q.ListCustomerOverviewAppointments(ctx, dbgen.ListCustomerOverviewAppointmentsParams{StoreID: env.Caller.StoreID, AppointmentIds: appointmentIDs})
		if err != nil {
			return nil, err
		}
		for _, row := range appointmentRows {
			appointments[row.ID] = CustomerNextAppointment{ID: row.ID, StartsAt: row.StartsAt.Time, Staff: staff.Ref{ID: row.StaffID, Name: row.StaffName}}
		}
	}
	for i, row := range rows {
		item := CustomerOverview{
			Customer:        *mask(env, views[i]),
			VisitCount:      row.VisitCount,
			LastVisitItems:  []string{},
			LastVisitStaff:  []staff.Ref{},
			NextAppointment: nil,
		}
		if row.FirstVisitDate != "" {
			item.FirstVisitDate = &row.FirstVisitDate
		}
		if row.LastVisitDate != "" {
			item.LastVisitDate = &row.LastVisitDate
		}
		if row.DaysSinceLastVisit >= 0 {
			item.DaysSinceLastVisit = &row.DaysSinceLastVisit
		}
		if row.CycleDays >= 0 {
			item.CycleDays = &row.CycleDays
		}
		if row.OverdueDays >= 0 {
			item.OverdueDays = &row.OverdueDays
		}
		if names := items[row.ID]; names != nil {
			item.LastVisitItems = names
		}
		if refs := lastStaff[row.ID]; refs != nil {
			item.LastVisitStaff = refs
		}
		if item.TotalSpend, err = moneyFromText(row.TotalSpend); err != nil {
			return nil, err
		}
		if item.StoredBalance, err = moneyFromText(row.StoredBalance); err != nil {
			return nil, err
		}
		item.CountRemaining = row.CountRemaining
		if row.NextAppointmentID != nil {
			if next, ok := appointments[*row.NextAppointmentID]; ok {
				item.NextAppointment = &next
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func getCustomerSegments(ctx context.Context, env ops.Env, in *CustomerSegmentsInput) (*CustomerSegments, error) {
	self, err := viewScope(env.Caller)
	if err != nil {
		return nil, err
	}
	today, start, end, err := overviewWindow(ctx, env)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CustomerOverviewSegments(ctx, dbgen.CustomerOverviewSegmentsParams{
		StoreID: env.Caller.StoreID, ResponsibleStaffID: in.ResponsibleStaffID, SelfID: self, Query: "",
		Today: pgDate(today), Now: pgTime(env.Now), TodayStart: pgTime(start), TodayEnd: pgTime(end),
		StaleDays: customerOverviewStaleDays, NewDays: customerOverviewNewDays,
	})
	if err != nil {
		return nil, err
	}
	storedTotal, err := moneyFromText(row.StoredTotal)
	if err != nil {
		return nil, err
	}
	return &CustomerSegments{
		All:      CustomerSegmentAll{Count: row.AllCount, VisitedThisMonth: row.VisitedThisMonth},
		Today:    CustomerSegmentToday{Count: row.TodayCount, Arrived: row.TodayArrived},
		Due:      CustomerSegmentDue{Count: row.DueCount, WithBalance: row.DueWithBalance},
		Balance:  CustomerSegmentBalance{Count: row.BalanceCount, StoredTotal: storedTotal},
		New:      CustomerSegmentNew{Count: row.NewCount, SingleVisit: row.NewSingleVisit},
		Sleeping: CustomerSegmentSleeping{Count: row.SleepingCount},
	}, nil
}
