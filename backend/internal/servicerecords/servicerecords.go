// Package servicerecords 管理单上服务项目行的服务记录：做成什么样、配方和顾客反馈。
package servicerecords

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// RecordServiceInput 是记一次服务做成什么样、配方和顾客反馈的参数；三项整体替换，没填的清空。
type RecordServiceInput struct {
	OrderID     uuid.UUID `json:"order_id"`
	OrderItemID uuid.UUID `json:"order_item_id"`
	Result      *string   `json:"result,omitempty" maxLength:"500" doc:"做成什么样，最多 500 字"`
	Formula     *string   `json:"formula,omitempty" maxLength:"500" doc:"配方，最多 500 字"`
	Feedback    *string   `json:"feedback,omitempty" enum:"satisfied,neutral,unsatisfied" doc:"顾客反馈：satisfied=满意，neutral=一般，unsatisfied=不满意"`
}

// ServiceRecord 是一次服务的做成什么样、配方和顾客反馈。
type ServiceRecord struct {
	ID          uuid.UUID `json:"id"`
	OrderID     uuid.UUID `json:"order_id"`
	OrderItemID uuid.UUID `json:"order_item_id"`
	Result      *string   `json:"result" nullable:"true"`
	Formula     *string   `json:"formula" nullable:"true"`
	Feedback    *string   `json:"feedback" nullable:"true" enum:"satisfied,neutral,unsatisfied"`
	RecordedBy  uuid.UUID `json:"recorded_by" doc:"最后一次写的人"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListServiceHistoryInput 是查看一位顾客服务历史的参数。
type ListServiceHistoryInput struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Limit      int32     `json:"limit,omitempty" minimum:"0" maximum:"100" doc:"不填为 20"`
	Offset     int32     `json:"offset,omitempty" minimum:"0"`
}

// ServiceHistory 是一页服务历史，以及不受分页限制的总数。
type ServiceHistory struct {
	Items []ServiceHistoryItem `json:"items"`
	Total int64                `json:"total"`
}

// ServiceHistoryItem 是一位顾客的一次服务项目行及其服务记录。
type ServiceHistoryItem struct {
	OrderID      uuid.UUID                `json:"order_id"`
	BusinessDate string                   `json:"business_date" format:"date"`
	OrderItemID  uuid.UUID                `json:"order_item_id"`
	Item         ServiceItemRef           `json:"item"`
	Category     *catalog.ItemCategoryRef `json:"category" doc:"项目类别，未归类时为空"`
	Staff        []ServiceStaff           `json:"staff" doc:"按岗位顺序的员工，空岗位不列"`
	Record       *ServiceRecord           `json:"record" doc:"这次服务的记录；还没记时为空"`
}

// TransformSchema 声明类别和服务记录或空值。
func (ServiceHistoryItem) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["category"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["category"], {Type: "null"}}}
	s.Properties["record"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["record"], {Type: "null"}}}
	return s
}

// ServiceItemRef 是服务记录里的项目引用。
type ServiceItemRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ServiceStaff 是服务项目行上一个岗位的员工。
type ServiceStaff struct {
	Position string    `json:"position" doc:"岗位名"`
	ID       uuid.UUID `json:"id" doc:"员工 ID"`
	Name     string    `json:"name" doc:"员工姓名"`
}

// Register 声明服务记录的读写操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[RecordServiceInput, ServiceRecord]{
		Name: "record_service", Summary: "记下一次服务做成什么样、配方和顾客反馈", Kind: ops.Write,
		Permission: perm.CustomerView, AI: ops.AIDraft, Handle: recordService,
	})
	ops.Register(r, ops.Operation[ListServiceHistoryInput, ServiceHistory]{
		Name: "list_service_history", Summary: "列出一位顾客每次来做的服务和服务记录", Kind: ops.Read,
		Permission: perm.CustomerView, AI: ops.AIDirect, Handle: listServiceHistory,
	})
}

// recordService 在锁住单和服务项目行后整体替换这条服务记录。
func recordService(ctx context.Context, env ops.Env, in *RecordServiceInput) (*ServiceRecord, error) {
	order, err := env.Q.GetServiceOrder(ctx, in.OrderID)
	if err != nil {
		return nil, rowError(err)
	}
	if order.StoreID != env.Caller.StoreID {
		return nil, ops.ErrNotFound
	}
	customer, err := customers.LockReference(ctx, env, order.CustomerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		return nil, err
	}
	order, err = env.Q.ShareServiceOrder(ctx, in.OrderID)
	if err != nil {
		return nil, rowError(err)
	}
	if order.CustomerID != customer.ID {
		return nil, ops.ErrNotFound
	}
	if order.Status != "open" && order.Status != "paid" {
		return nil, ops.Rule("这张单已撤单或已合并，不能记服务")
	}
	item, err := env.Q.GetServiceOrderItem(ctx, dbgen.GetServiceOrderItemParams{StoreID: order.StoreID, ID: in.OrderItemID})
	if err != nil {
		return nil, rowError(err)
	}
	if item.OrderID != order.ID {
		return nil, ops.ErrNotFound
	}
	if item.RemovedAt.Valid {
		return nil, ops.Rule("项目已去掉")
	}
	if item.ItemKind != "service" {
		return nil, ops.Rule("卖品不记服务")
	}
	result, formula := text(in.Result), text(in.Formula)
	feedback := text(in.Feedback)
	if result == nil && formula == nil && feedback == nil {
		return nil, ops.Rule("至少填写做成什么样、配方或顾客反馈中的一项")
	}

	if err := env.Q.LockServiceItem(ctx, dbgen.LockServiceItemParams{TenantID: env.Caller.TenantID, OrderItemID: item.ID}); err != nil {
		return nil, err
	}
	var before *ServiceRecord
	old, err := env.Q.LockServiceRecord(ctx, dbgen.LockServiceRecordParams{TenantID: env.Caller.TenantID, OrderItemID: item.ID})
	switch {
	case err == nil:
		view := recordView(order.ID, old)
		before = &view
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	saved, err := env.Q.UpsertServiceRecord(ctx, dbgen.UpsertServiceRecordParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderItemID: item.ID,
		Result: result, Formula: formula, Feedback: feedback, RecordedBy: env.Caller.StaffID,
		CreatedAt: pgtype.Timestamptz{Time: env.Now, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	after := recordView(order.ID, saved)
	if err := audit.Record(ctx, env, "record_service", audit.OrderTarget(order.ID, order.CustomerID), before, &after); err != nil {
		return nil, err
	}
	return &after, nil
}

// listServiceHistory 按顾客、本店和已结账筛选服务项目行，子项按批读取。
func listServiceHistory(ctx context.Context, env ops.Env, in *ListServiceHistoryInput) (*ServiceHistory, error) {
	if err := customers.CheckViewable(ctx, env, in.CustomerID); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	total, err := env.Q.CountServiceHistory(ctx, dbgen.CountServiceHistoryParams{StoreID: env.Caller.StoreID, CustomerID: in.CustomerID})
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListServiceHistory(ctx, dbgen.ListServiceHistoryParams{
		StoreID: env.Caller.StoreID, CustomerID: in.CustomerID, RowLimit: limit, RowOffset: in.Offset,
	})
	if err != nil {
		return nil, err
	}
	itemIDs := make([]uuid.UUID, len(rows))
	categoryIDs := []uuid.UUID{}
	for i, row := range rows {
		itemIDs[i] = row.OrderItemID
		if row.CategoryID != nil {
			categoryIDs = append(categoryIDs, *row.CategoryID)
		}
	}
	categories := map[uuid.UUID]catalog.ItemCategoryRef{}
	if len(categoryIDs) > 0 {
		categories, err = catalog.CategoryReferences(ctx, env.Q, env.Caller.StoreID, categoryIDs)
		if err != nil {
			return nil, err
		}
	}
	staffByItem := map[uuid.UUID][]ServiceStaff{}
	records := map[uuid.UUID]ServiceRecord{}
	if len(itemIDs) > 0 {
		staffRows, err := env.Q.ListServiceHistoryStaff(ctx, dbgen.ListServiceHistoryStaffParams{StoreID: env.Caller.StoreID, ItemIds: itemIDs})
		if err != nil {
			return nil, err
		}
		for _, row := range staffRows {
			staffByItem[row.OrderItemID] = append(staffByItem[row.OrderItemID], ServiceStaff{Position: row.PositionName, ID: row.StaffID, Name: row.StaffName})
		}
		recordRows, err := env.Q.ListServiceRecordsByItems(ctx, dbgen.ListServiceRecordsByItemsParams{TenantID: env.Caller.TenantID, ItemIds: itemIDs})
		if err != nil {
			return nil, err
		}
		for _, row := range recordRows {
			records[row.OrderItemID] = recordView(uuid.Nil, row)
		}
	}
	items := make([]ServiceHistoryItem, 0, len(rows))
	for _, row := range rows {
		item := ServiceHistoryItem{
			OrderID: row.OrderID, BusinessDate: row.BusinessDate.Time.Format(time.DateOnly), OrderItemID: row.OrderItemID,
			Item: ServiceItemRef{ID: row.ItemID, Name: row.ItemName}, Staff: staffByItem[row.OrderItemID],
		}
		if item.Staff == nil {
			item.Staff = []ServiceStaff{}
		}
		if row.CategoryID != nil {
			if category, ok := categories[*row.CategoryID]; ok {
				item.Category = &category
			}
		}
		if record, ok := records[row.OrderItemID]; ok {
			record.OrderID = row.OrderID
			item.Record = &record
		}
		items = append(items, item)
	}
	return &ServiceHistory{Items: items, Total: total}, nil
}

// recordView 把数据库里的服务记录转换为返回视图。
func recordView(orderID uuid.UUID, row dbgen.ServiceRecord) ServiceRecord {
	return ServiceRecord{
		ID: row.ID, OrderID: orderID, OrderItemID: row.OrderItemID,
		Result: row.Result, Formula: row.Formula, Feedback: row.Feedback,
		RecordedBy: row.RecordedBy, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

// text 把空字符串和只有空白的字符串当成没填；非空值原样保留。
func text(v *string) *string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return v
}

// rowError 把不存在转换成业务错误。
func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}
