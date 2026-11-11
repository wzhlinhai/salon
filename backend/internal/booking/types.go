// Package booking 管理预约与私约占位：员工代顾客预约、改约、取消、状态流转和轮牌占用。
package booking

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/staff"
)

const (
	// KindAppointment 是顾客预约。
	KindAppointment = "appointment"
	// KindBlock 是员工自己登记的私约占位。
	KindBlock = "block"
)

const (
	// StatusPending：顾客自助预约的初始状态，等待门店确认。
	StatusPending = "pending"
	// StatusConfirmed：已确认的预约或有效的私约占位。
	StatusConfirmed = "confirmed"
	// StatusCompleted：顾客已完成服务。
	StatusCompleted = "completed"
	// StatusCancelled：预约或占位已取消。
	StatusCancelled = "cancelled"
	// StatusNoShow：已确认的预约顾客未到。
	StatusNoShow = "no_show"
)

const (
	// CancelReasonCustomer：顾客取消。
	CancelReasonCustomer = "顾客取消"
	// CancelReasonStore：门店取消。
	CancelReasonStore = "门店取消"
	// CancelReasonReschedule：因为改约而取消。
	CancelReasonReschedule = "改约"
	// CancelReasonOther：其他原因，需要填写说明。
	CancelReasonOther = "其他"
)

// AppointmentCustomer 是预约关联顾客的最小视图；没有查看范围时只保留 ID。
type AppointmentCustomer struct {
	ID          uuid.UUID `json:"id"`
	Name        *string   `json:"name" nullable:"true"`
	Phone       *string   `json:"phone" nullable:"true" doc:"按 customer.full_phone 权限打码"`
	PhoneMasked bool      `json:"phone_masked"`
}

// AppointmentItem 是预约选中的项目及服务时长。
type AppointmentItem struct {
	ItemID          uuid.UUID `json:"item_id"`
	Name            string    `json:"name"`
	Position        int       `json:"position"`
	DurationMinutes int       `json:"duration_minutes" doc:"项目服务时长，0 表示未设置"`
}

// TransformSchema 声明创建事件没有改前发型师。
func (AppointmentEvent) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["from_staff"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["from_staff"], {Type: "null"}}}
	return s
}

// AppointmentEvent 是一次只追加的预约/占位变化。
type AppointmentEvent struct {
	FromStartsAt *time.Time `json:"from_starts_at" nullable:"true"`
	FromEndsAt   *time.Time `json:"from_ends_at" nullable:"true"`
	ToStartsAt   time.Time  `json:"to_starts_at"`
	ToEndsAt     time.Time  `json:"to_ends_at"`
	FromStaff    *staff.Ref `json:"from_staff"`
	ToStaff      staff.Ref  `json:"to_staff"`
	Reason       *string    `json:"reason" nullable:"true"`
	Note         *string    `json:"note" nullable:"true"`
	ID           uuid.UUID  `json:"id"`
	Kind         string     `json:"kind" enum:"create,reschedule,confirm,complete,cancel,no_show"`
	Actor        staff.Ref  `json:"actor"`
	CreatedAt    time.Time  `json:"created_at"`
}

// TransformSchema 声明私约占位没有关联顾客。
func (Appointment) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["customer"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["customer"], {Type: "null"}}}
	return s
}

// Appointment 是预约或私约占位的完整视图。
type Appointment struct {
	Customer  *AppointmentCustomer `json:"customer" doc:"私约占位不关联顾客"`
	OrderID   *uuid.UUID           `json:"order_id" nullable:"true"`
	Note      *string              `json:"note" nullable:"true"`
	ID        uuid.UUID            `json:"id"`
	Kind      string               `json:"kind" enum:"appointment,block"`
	Status    string               `json:"status" enum:"pending,confirmed,completed,cancelled,no_show"`
	StartsAt  time.Time            `json:"starts_at"`
	EndsAt    time.Time            `json:"ends_at"`
	Staff     staff.Ref            `json:"staff"`
	CreatedBy staff.Ref            `json:"created_by"`
	CreatedAt time.Time            `json:"created_at"`
	Items     []AppointmentItem    `json:"items"`
	Events    []AppointmentEvent   `json:"events"`
}

// AppointmentList 是某个日期范围内的预约和占位。
type AppointmentList struct {
	Entries []Appointment `json:"entries"`
}

// AppointmentItemInput 是预约选择的一个项目。
type AppointmentItemInput struct {
	ItemID uuid.UUID `json:"item_id"`
}

// CreateAppointmentInput 是前台代顾客预约的参数；不提供顾客时新建未识别顾客。
type CreateAppointmentInput struct {
	CustomerID    *uuid.UUID             `json:"customer_id,omitempty"`
	CustomerName  *string                `json:"customer_name,omitempty" maxLength:"40"`
	CustomerPhone *string                `json:"customer_phone,omitempty" maxLength:"30"`
	Items         []AppointmentItemInput `json:"items,omitempty" maxItems:"20"`
	Note          *string                `json:"note,omitempty" maxLength:"200"`
	StaffID       uuid.UUID              `json:"staff_id"`
	StartsAt      time.Time              `json:"starts_at"`
	EndsAt        *time.Time             `json:"ends_at,omitempty" doc:"不填时按项目时长之和或门店默认时长计算"`
}

// RescheduleAppointmentInput 是修改预约时间或发型师的参数。
type RescheduleAppointmentInput struct {
	EndsAt   *time.Time `json:"ends_at,omitempty" doc:"不填时保持原时长"`
	Reason   *string    `json:"reason,omitempty" maxLength:"200"`
	StartsAt *time.Time `json:"starts_at,omitempty" doc:"不填时保持原开始时间"`
	ID       uuid.UUID  `json:"id"`
	StaffID  *uuid.UUID `json:"staff_id,omitempty" doc:"不填时保持原发型师"`
}

// CancelAppointmentInput 是取消预约的参数，必须选择原因。
type CancelAppointmentInput struct {
	Note   *string   `json:"note,omitempty" maxLength:"200" doc:"原因为其他时的说明"`
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason" minLength:"1" enum:"顾客取消,门店取消,改约,其他"`
}

// AppointmentStatusInput 是确认、完成或记爽约的参数。
type AppointmentStatusInput struct {
	ID uuid.UUID `json:"id"`
}

// CompleteAppointmentInput 是完成预约并可选关联一张同顾客的单。
type CompleteAppointmentInput struct {
	OrderID *uuid.UUID `json:"order_id,omitempty"`
	ID      uuid.UUID  `json:"id"`
}

// CreateBlockInput 是员工登记私约占位的参数。
type CreateBlockInput struct {
	Note     *string   `json:"note,omitempty" maxLength:"200"`
	StaffID  uuid.UUID `json:"staff_id"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// CancelBlockInput 是取消私约占位的参数。
type CancelBlockInput struct {
	ID uuid.UUID `json:"id"`
}

// ListAppointmentsInput 是按营业日范围查看预约的参数。
type ListAppointmentsInput struct {
	StaffID *uuid.UUID `json:"staff_id,omitempty"`
	From    string     `json:"from" format:"date"`
	To      string     `json:"to" format:"date"`
}

// GetAppointmentInput 是查看一条预约或占位的参数。
type GetAppointmentInput struct {
	ID uuid.UUID `json:"id"`
}
