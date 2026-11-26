// Package audit 记录业务操作的改前改后，并提供查询操作。
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
)

// Target 是日志目标的种类、标识及其关联顾客。
type Target struct {
	kind       string
	id         uuid.UUID
	customerID *uuid.UUID
}

// CustomerTarget 创建顾客日志目标。
func CustomerTarget(id uuid.UUID) Target { return Target{kind: "customer", id: id, customerID: &id} }

// StaffTarget 创建员工日志目标。
func StaffTarget(id uuid.UUID) Target { return Target{kind: "staff", id: id} }

// RoleTarget 创建角色日志目标。
func RoleTarget(id uuid.UUID) Target { return Target{kind: "role", id: id} }

// CustomerSourceTarget 创建顾客来源日志目标。
func CustomerSourceTarget(id uuid.UUID) Target { return Target{kind: "customer_source", id: id} }

// ItemTarget 创建项目日志目标。
func ItemTarget(id uuid.UUID) Target { return Target{kind: "item", id: id} }

// ItemCategoryTarget 创建项目类别日志目标。
func ItemCategoryTarget(id uuid.UUID) Target { return Target{kind: "item_category", id: id} }

// DayCloseTarget 创建日结快照日志目标。
func DayCloseTarget(id uuid.UUID) Target { return Target{kind: "day_close", id: id} }

// ClosingTarget 创建封账或撤销封账事件日志目标。
func ClosingTarget(id uuid.UUID) Target { return Target{kind: "closing", id: id} }

// Record 在业务操作的同一事务中追加日志；nil 表示新建前或删除后不存在；手机号必须用键名 phone 或以 _phone 结尾。
func Record(ctx context.Context, env ops.Env, action string, target Target, before, after any) error {
	params, err := prepare(env, action, target, before, after)
	if err != nil {
		return err
	}
	switch {
	case env.Caller.StaffID != uuid.Nil:
		params.ActorID = &env.Caller.StaffID
	case env.Caller.CustomerID != nil:
		params.ActorCustomerID = env.Caller.CustomerID
	default:
		return fmt.Errorf("audit: caller has no actor")
	}
	return env.Q.AddAuditLog(ctx, params)
}

// RecordSystem 记录没有员工或顾客操作者的系统操作，例如后台自动锁定工资月份。
func RecordSystem(ctx context.Context, env ops.Env, action string, target Target, before, after any) error {
	params, err := prepare(env, action, target, before, after)
	if err != nil {
		return err
	}
	return env.Q.AddAuditLog(ctx, params)
}

func prepare(env ops.Env, action string, target Target, before, after any) (dbgen.AddAuditLogParams, error) {
	if err := checkPhoneKeys(before); err != nil {
		return dbgen.AddAuditLogParams{}, err
	}
	if err := checkPhoneKeys(after); err != nil {
		return dbgen.AddAuditLogParams{}, err
	}
	var oldJSON, newJSON []byte
	var err error
	if before != nil {
		oldJSON, err = json.Marshal(before)
		if err != nil {
			return dbgen.AddAuditLogParams{}, err
		}
	}
	if after != nil {
		newJSON, err = json.Marshal(after)
		if err != nil {
			return dbgen.AddAuditLogParams{}, err
		}
	}
	return dbgen.AddAuditLogParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
		DraftID: env.DraftID, Action: action, TargetID: target.id, TargetKind: target.kind, CustomerID: target.customerID, Before: oldJSON, After: newJSON,
	}, nil
}

// checkPhoneKeys 遍历日志值，确保手机号字段使用读取时会打码的键名。
func checkPhoneKeys(v any) error {
	return checkPhoneValue(reflect.ValueOf(v), 0)
}

// checkPhoneValue 限制嵌套深度并检查动态值，匿名嵌入的结构体继续展开。
func checkPhoneValue(v reflect.Value, depth int) error {
	if depth > 64 {
		return fmt.Errorf("audit: value nested too deeply")
	}
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return checkPhoneValue(v.Elem(), depth+1)
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			field := t.Field(i)
			serialised, err := privacy.PhoneField(t, field)
			if err != nil {
				return err
			}
			if !serialised {
				continue
			}
			if err := checkPhoneValue(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := checkPhoneValue(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			key, child := iter.Key(), iter.Value()
			if err := checkPhoneValue(child, depth+1); err != nil {
				return err
			}
			value := child
			for (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) && !value.IsNil() {
				value = value.Elem()
			}
			if key.Kind() == reflect.String && value.Kind() == reflect.String && privacy.PhoneKeyword(key.String()) && !privacy.PhoneKey(key.String()) {
				return fmt.Errorf("audit: map key %q contains a phone number; phone numbers must use the key \"phone\" or a key ending in \"_phone\" so they can be masked", key.String())
			}
		}
	}
	return nil
}

// Actor 是操作的发起员工或顾客本人。
type Actor struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Kind string    `json:"kind" enum:"staff,customer"`
}

// TransformSchema 声明系统操作日志没有操作者。
func (Entry) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["actor"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["actor"], {Type: "null"}}}
	return s
}

// Entry 是一次操作的记录，改前改后按调用者权限打码手机号。
type Entry struct {
	DraftID    *uuid.UUID      `json:"draft_id"`
	ID         uuid.UUID       `json:"id"`
	Action     string          `json:"action"`
	TargetID   uuid.UUID       `json:"target_id"`
	TargetKind string          `json:"target_kind"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	Actor      *Actor          `json:"actor"`
	At         time.Time       `json:"at"`
}

// ListAuditLogInput 是 list_audit_log 的参数。
type ListAuditLogInput struct {
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty" doc:"上一页最后一条的 ID，只看比它更早的；不填为最新一页"`
	TargetID *uuid.UUID `json:"target_id,omitempty" doc:"只看这个目标的修改，不填为全部"`
}

// LogList 是 list_audit_log 的返回值，最新的操作在前。
type LogList struct {
	Entries []Entry `json:"entries"`
}

// Register 声明操作日志的读操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListAuditLogInput, LogList]{
		Name: "list_audit_log", Summary: "查看业务操作的修改记录：谁、何时、改了什么、改前改后", Kind: ops.Read,
		Permission: perm.AuditLogView, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *ListAuditLogInput) (*LogList, error) {
			customerView, self := env.Caller.ScopeFilter(perm.CustomerView)
			limit := in.Limit
			if limit == 0 {
				limit = 50
			}
			rows, err := env.Q.ListAuditLog(ctx, dbgen.ListAuditLogParams{
				StoreID: env.Caller.StoreID, TargetID: in.TargetID, BeforeID: in.BeforeID, RowLimit: limit,
				CustomerView: customerView, CustomerSelfID: self, SettingsManage: env.Caller.Has(perm.SettingsManage), PayrollManage: env.Caller.Has(perm.PayrollManage),
			})
			if err != nil {
				return nil, err
			}
			out := &LogList{Entries: make([]Entry, 0, len(rows))}
			for _, row := range rows {
				a := row.AuditLog
				if !env.Caller.Has(perm.CustomerFullPhone) {
					a.Before, err = maskPhones(a.Before)
					if err != nil {
						return nil, err
					}
					a.After, err = maskPhones(a.After)
					if err != nil {
						return nil, err
					}
				}
				actorID := a.ActorCustomerID
				if actorID == nil {
					actorID = a.ActorID
				}
				var actor *Actor
				if actorID != nil {
					actor = &Actor{ID: *actorID, Name: row.ActorName, Kind: row.ActorKind}
				}
				out.Entries = append(out.Entries, Entry{DraftID: a.DraftID, ID: a.ID, Action: a.Action, TargetID: a.TargetID, TargetKind: a.TargetKind, Before: a.Before, After: a.After, Actor: actor, At: a.At.Time})
			}
			return out, nil
		},
	})
}

// maskPhones 递归打码日志中 phone 或以 _phone 结尾的字符串值，保留其他字段和数字精度。
func maskPhones(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	maskPhoneValues(value)
	return json.Marshal(value)
}

// maskPhoneValues 遍历对象和数组，在任意嵌套深度使用相同的手机号打码规则。
func maskPhoneValues(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if phone, ok := child.(string); ok && privacy.PhoneKey(key) {
				v[key] = privacy.MaskPhone(phone)
			} else {
				maskPhoneValues(child)
			}
		}
	case []any:
		for _, child := range v {
			maskPhoneValues(child)
		}
	}
}

// CardTypeTarget 创建卡类型日志目标。
func CardTypeTarget(id uuid.UUID) Target { return Target{kind: "card_type", id: id} }

// CardTarget 创建关联顾客的卡日志目标。
func CardTarget(id, customerID uuid.UUID) Target {
	return Target{kind: "card", id: id, customerID: &customerID}
}

// CardUpgradeRuleTarget 创建不关联顾客的升级规则日志目标。
func CardUpgradeRuleTarget(id uuid.UUID) Target { return Target{kind: "card_upgrade_rule", id: id} }

// OrderTarget 创建关联顾客的单据日志目标。
func OrderTarget(id, customerID uuid.UUID) Target {
	return Target{kind: "order", id: id, customerID: &customerID}
}

// RedPacketTarget 创建关联顾客的红包日志目标。
func RedPacketTarget(id, customerID uuid.UUID) Target {
	return Target{kind: "red_packet", id: id, customerID: &customerID}
}

// AftersaleTarget 创建不关联顾客的售后单日志目标。
func AftersaleTarget(id uuid.UUID) Target { return Target{kind: "aftersale", id: id} }

// DebtTarget 创建关联顾客的欠款日志目标。
func DebtTarget(id, customerID uuid.UUID) Target {
	return Target{kind: "debt", id: id, customerID: &customerID}
}

// CommissionSchemeTarget 创建不关联顾客的提成方案日志目标。
func CommissionSchemeTarget(id uuid.UUID) Target { return Target{kind: "commission_scheme", id: id} }

// BaseSalaryTarget 创建以员工标识为目标的底薪日志目标。
func BaseSalaryTarget(staffID uuid.UUID) Target { return Target{kind: "base_salary", id: staffID} }

// PayrollAdjustmentTarget 创建不关联顾客的加减项日志目标。
func PayrollAdjustmentTarget(id uuid.UUID) Target { return Target{kind: "payroll_adjustment", id: id} }

// PayrollMonthTarget 创建不关联顾客的工资锁月日志目标。
func PayrollMonthTarget(id uuid.UUID) Target { return Target{kind: "payroll_month", id: id} }

// ShiftTarget 创建不关联顾客的班次日志目标。
func ShiftTarget(id uuid.UUID) Target { return Target{kind: "shift", id: id} }

// StaffShiftTarget 创建以员工标识为目标的员工班次日志目标。
func StaffShiftTarget(staffID uuid.UUID) Target { return Target{kind: "staff_shift", id: staffID} }

// PunchTarget 创建不关联顾客的打卡日志目标。
func PunchTarget(id uuid.UUID) Target { return Target{kind: "punch", id: id} }

// AttendanceApprovalTarget 创建不关联顾客的考勤核准日志目标。
func AttendanceApprovalTarget(id uuid.UUID) Target {
	return Target{kind: "attendance_approval", id: id}
}

// CollectionTarget 创建不关联顾客的收款日志目标。
func CollectionTarget(id uuid.UUID) Target { return Target{kind: "collection", id: id} }

// CouponRedemptionTarget 创建不关联顾客的团购核销日志目标。
func CouponRedemptionTarget(id uuid.UUID) Target { return Target{kind: "coupon_redemption", id: id} }

// GroupbuyDealTarget 创建团购商品日志目标。
func GroupbuyDealTarget(id uuid.UUID) Target { return Target{kind: "groupbuy_deal", id: id} }

// GroupbuySettlementTarget 创建团购月结算日志目标。
func GroupbuySettlementTarget(id uuid.UUID) Target {
	return Target{kind: "groupbuy_settlement", id: id}
}

// RotationQueueTarget 创建不关联顾客的轮牌队列或轮牌配置日志目标。
func RotationQueueTarget(id uuid.UUID) Target { return Target{kind: "rotation_queue", id: id} }

// ImportBatchTarget 创建不关联顾客的导入批次日志目标。
func ImportBatchTarget(id uuid.UUID) Target { return Target{kind: "import_batch", id: id} }

// DataExportTarget 创建不关联顾客的数据导出日志目标。
func DataExportTarget(id uuid.UUID) Target { return Target{kind: "data_export", id: id} }

// AppointmentTarget 创建关联顾客的预约日志目标。
func AppointmentTarget(id, customerID uuid.UUID) Target {
	return Target{kind: "appointment", id: id, customerID: &customerID}
}

// AppointmentBlockTarget 创建不关联顾客的私约占位日志目标。
func AppointmentBlockTarget(id uuid.UUID) Target { return Target{kind: "appointment_block", id: id} }

// MaskPhones 对其他审计视图复用相同的 JSON 手机号打码规则。
func MaskPhones(raw json.RawMessage) (json.RawMessage, error) { return maskPhones(raw) }
