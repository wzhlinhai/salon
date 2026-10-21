// Package audit 记录业务操作的改前改后，并提供查询操作。
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

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

// Record 在业务操作的同一事务中追加日志；nil 表示新建前或删除后不存在；手机号必须用键名 phone。
func Record(ctx context.Context, env ops.Env, action string, target Target, before, after any) error {
	if err := checkPhoneKeys(before); err != nil {
		return err
	}
	if err := checkPhoneKeys(after); err != nil {
		return err
	}
	var oldJSON, newJSON []byte
	var err error
	if before != nil {
		oldJSON, err = json.Marshal(before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		newJSON, err = json.Marshal(after)
		if err != nil {
			return err
		}
	}
	return env.Q.AddAuditLog(ctx, dbgen.AddAuditLogParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
		Action: action, TargetID: target.id, TargetKind: target.kind, CustomerID: target.customerID, Before: oldJSON, After: newJSON, ActorID: env.Caller.StaffID,
	})
}

// checkPhoneKeys 遍历日志值，确保手机号字段使用读取时会打码的 phone 键名。
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
			fieldType := field.Type
			if fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}
			if !field.IsExported() && (!field.Anonymous || fieldType.Kind() != reflect.Struct) {
				continue
			}
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			key, options, _ := strings.Cut(tag, ",")
			if key == "" {
				key = field.Name
			}
			if strings.Contains(strings.ToLower(field.Name), "phone") && fieldType.Kind() == reflect.String {
				if key != "phone" {
					return fmt.Errorf("audit: field %s.%s is logged as %q; phone numbers must use the key \"phone\" so list_audit_log can mask them", t.Name(), field.Name, key)
				}
				if strings.Contains(","+options+",", ",string,") {
					return fmt.Errorf("audit: field %s.%s uses the JSON string option; quoted phone numbers cannot be masked by list_audit_log", t.Name(), field.Name)
				}
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
			if key.Kind() == reflect.String && strings.Contains(strings.ToLower(key.String()), "phone") && value.Kind() == reflect.String && key.String() != "phone" {
				return fmt.Errorf("audit: map key %q contains a phone number; phone numbers must use the key \"phone\" so list_audit_log can mask them", key.String())
			}
		}
	}
	return nil
}

// Actor 是操作的发起员工。
type Actor struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Entry 是一次操作的记录，改前改后按调用者权限打码手机号。
type Entry struct {
	ID         uuid.UUID       `json:"id"`
	Action     string          `json:"action"`
	TargetID   uuid.UUID       `json:"target_id"`
	TargetKind string          `json:"target_kind"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	Actor      Actor           `json:"actor"`
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
				CustomerView: customerView, CustomerSelfID: self,
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
				out.Entries = append(out.Entries, Entry{ID: a.ID, Action: a.Action, TargetID: a.TargetID, TargetKind: a.TargetKind, Before: a.Before, After: a.After, Actor: Actor{ID: a.ActorID, Name: row.ActorName}, At: a.At.Time})
			}
			return out, nil
		},
	})
}

// maskPhones 递归打码日志中名为 phone 的字符串值，保留其他字段和数字精度。
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
			if phone, ok := child.(string); key == "phone" && ok {
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
