// Package rotation 管理电子轮牌：多个队列、每日顺序、点客、跳牌、盖牌、临时休息、拖动和结账推进。
package rotation

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/staff"
)

const (
	// StatusOff：今天还没到岗，或已经离岗。
	StatusOff = "off"
	// StatusWaiting：在队列中等待接客。
	StatusWaiting = "waiting"
	// StatusServing：正在服务。
	StatusServing = "serving"
	// StatusResting：临时休息，到点自动恢复等待。
	StatusResting = "resting"
	// StatusCovered：盖牌，保留位置但不能接客。
	StatusCovered = "covered"
)

const (
	// SkipReasonServing：员工正在服务。
	SkipReasonServing = "服务中"
	// SkipReasonRest：员工在休息。
	SkipReasonRest = "休息"
	// SkipReasonLate 是“迟到”。
	SkipReasonLate = "迟到"
	// SkipReasonDesignated 是“顾客指定他人”。
	SkipReasonDesignated = "顾客指定他人"
	// SkipReasonOther 是“其他”，需要填写说明。
	SkipReasonOther = "其他"
	// AutoSkipReason 是点牌跳牌自动跳过时的原因。
	AutoSkipReason = "点客服务中"
	// BookingOccupiedReason 是员工被预约或私约占位占住时自动跳过的原因。
	BookingOccupiedReason = "预约/私约占位中"
)

// Turn 是某个营业日队列中的一名员工。
type Turn struct {
	RestUntil         *time.Time `json:"rest_until" nullable:"true" doc:"临时休息结束时刻"`
	ServingOrderID    *uuid.UUID `json:"serving_order_id" nullable:"true" doc:"正在服务的单"`
	Staff             staff.Ref  `json:"staff"`
	Status            string     `json:"status" enum:"off,waiting,serving,resting,covered"`
	Position          int        `json:"position"`
	HalfPending       bool       `json:"half_pending" doc:"已经记过一次半轮，下一次半轮排到队尾"`
	ServingDesignated bool       `json:"serving_designated" doc:"正在服务的是点客"`
}

// Queue 是一个轮牌队列。
type Queue struct {
	CreatedBy staff.Ref `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	Members   []Member  `json:"members"`
}

// Member 是轮牌队列的成员及其默认顺序。
type Member struct {
	Staff     staff.Ref `json:"staff"`
	SortOrder int       `json:"sort_order"`
}

// QueueList 是本店全部轮牌队列。
type QueueList struct {
	Entries []Queue `json:"entries"`
}

// Rotation 是某个队列在一个营业日的有序轮牌。
type Rotation struct {
	Queue        Queue  `json:"queue"`
	BusinessDate string `json:"business_date" format:"date"`
	Entries      []Turn `json:"entries"`
}

// CreateRotationQueueInput 是新建轮牌队列的参数。
type CreateRotationQueueInput struct {
	Name string `json:"name" minLength:"1" maxLength:"20"`
}

// UpdateRotationQueueInput 是改名或停用轮牌队列的参数。
type UpdateRotationQueueInput struct {
	Name   *string   `json:"name,omitempty" maxLength:"20"`
	ID     uuid.UUID `json:"id"`
	Active *bool     `json:"active,omitempty"`
}

// SetQueueMembersInput 是替换队列成员及默认顺序的参数。
type SetQueueMembersInput struct {
	QueueID  uuid.UUID   `json:"queue_id"`
	StaffIDs []uuid.UUID `json:"staff_ids" maxItems:"100"`
}

// ListRotationQueuesInput 是无需参数的队列查询。
type ListRotationQueuesInput struct{}

// GetRotationInput 是查看某个队列某个营业日轮牌的参数。
type GetRotationInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	Date    string    `json:"date,omitempty" format:"date" doc:"不填为今天"`
}

// JoinRotationInput 是员工到岗加入轮牌的参数。
type JoinRotationInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// LeaveRotationInput 是员工离岗的参数。
type LeaveRotationInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// AssignTurnInput 是点客或轮牌接客的参数。
type AssignTurnInput struct {
	OrderID    *uuid.UUID `json:"order_id,omitempty" doc:"正在服务的单，收银结账后据此推进轮牌"`
	QueueID    uuid.UUID  `json:"queue_id"`
	StaffID    *uuid.UUID `json:"staff_id,omitempty" doc:"点客时指定员工；不填为轮牌自动取第一个等待的员工"`
	Designated bool       `json:"designated,omitempty" doc:"点客为 true，轮牌为 false"`
}

// ReleaseTurnInput 是撤销一次接客、不消耗轮牌的参数。
type ReleaseTurnInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// SkipStaffInput 是跳过员工的参数。
type SkipStaffInput struct {
	Note    *string   `json:"note,omitempty" maxLength:"100" doc:"原因为其他时的说明"`
	Reason  string    `json:"reason" enum:"服务中,休息,迟到,顾客指定他人,其他"`
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// CoverTurnInput 是盖牌的参数。
type CoverTurnInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// UncoverTurnInput 是取消盖牌的参数。
type UncoverTurnInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
}

// RestStaffInput 是临时休息的参数。
type RestStaffInput struct {
	QueueID uuid.UUID `json:"queue_id"`
	StaffID uuid.UUID `json:"staff_id"`
	Minutes int       `json:"minutes" minimum:"1" maximum:"240"`
}

// DragTurnInput 是手动拖动顺序的参数。
type DragTurnInput struct {
	Reason     string    `json:"reason" minLength:"1" maxLength:"200"`
	QueueID    uuid.UUID `json:"queue_id"`
	StaffID    uuid.UUID `json:"staff_id"`
	ToPosition int       `json:"to_position" minimum:"1"`
}

// SetHalfTurnItemsInput 是按项目记半轮的项目清单。
type SetHalfTurnItemsInput struct {
	ItemIDs []uuid.UUID `json:"item_ids" maxItems:"200"`
}

// HalfTurnItem 是记半轮的项目。
type HalfTurnItem struct {
	ItemID uuid.UUID `json:"item_id"`
	Name   string    `json:"name"`
}

// HalfTurnItemList 是记半轮的项目清单。
type HalfTurnItemList struct {
	Entries []HalfTurnItem `json:"entries"`
}

// ListRotationEventsInput 是按日期范围查看轮牌事件的参数。
type ListRotationEventsInput struct {
	QueueID *uuid.UUID `json:"queue_id,omitempty"`
	From    string     `json:"from" format:"date"`
	To      string     `json:"to" format:"date"`
}

// RotationEvent 是一条只追加的轮牌记录。
type RotationEvent struct {
	Staff        *staff.Ref `json:"staff"`
	Reason       *string    `json:"reason" nullable:"true"`
	Note         *string    `json:"note" nullable:"true"`
	OrderID      *uuid.UUID `json:"order_id" nullable:"true"`
	Designated   *bool      `json:"designated" nullable:"true"`
	FromPosition *int       `json:"from_position" nullable:"true"`
	ToPosition   *int       `json:"to_position" nullable:"true"`
	Minutes      *int       `json:"minutes" nullable:"true"`
	ID           uuid.UUID  `json:"id"`
	QueueID      uuid.UUID  `json:"queue_id"`
	BusinessDate string     `json:"business_date" format:"date"`
	Kind         string     `json:"kind" enum:"join,leave,assign,release,skip,auto_skip,cover,uncover,rest,rest_end,drag,advance_whole,advance_half,advance_designated_no_turn"`
	Actor        staff.Ref  `json:"actor"`
	CreatedAt    time.Time  `json:"created_at"`
}

// RotationEventList 是轮牌事件列表。
type RotationEventList struct {
	Entries []RotationEvent `json:"entries"`
}

// GetRotationStatsInput 是按日期范围查看点客与轮牌接客统计的参数。
type GetRotationStatsInput struct {
	From string `json:"from" format:"date"`
	To   string `json:"to" format:"date"`
}

// StaffRotationStat 是一名员工的接客数。
type StaffRotationStat struct {
	Staff      staff.Ref `json:"staff"`
	Designated int       `json:"designated" doc:"点客接客数"`
	Rotation   int       `json:"rotation" doc:"轮牌接客数"`
	Total      int       `json:"total"`
}

// RotationStats 是本店点客与轮牌接客统计。
type RotationStats struct {
	From       string              `json:"from" format:"date"`
	To         string              `json:"to" format:"date"`
	Entries    []StaffRotationStat `json:"entries"`
	Designated int                 `json:"designated"`
	Rotation   int                 `json:"rotation"`
	Total      int                 `json:"total"`
}
