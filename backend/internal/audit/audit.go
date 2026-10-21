// Package audit 记录业务操作的改前改后，并提供查询操作。
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
)

// Record 在业务操作的同一事务中追加日志；nil 表示新建前或删除后不存在。
func Record(ctx context.Context, env ops.Env, action string, targetID uuid.UUID, before, after any) error {
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
		Action: action, TargetID: targetID, Before: oldJSON, After: newJSON, ActorID: env.Caller.StaffID,
	})
}

// Actor 是操作的发起员工。
type Actor struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Entry 是一次操作的记录，改前改后按调用者权限打码手机号。
type Entry struct {
	ID       uuid.UUID       `json:"id"`
	Action   string          `json:"action"`
	TargetID uuid.UUID       `json:"target_id"`
	Before   json.RawMessage `json:"before"`
	After    json.RawMessage `json:"after"`
	Actor    Actor           `json:"actor"`
	At       time.Time       `json:"at"`
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
				out.Entries = append(out.Entries, Entry{ID: a.ID, Action: a.Action, TargetID: a.TargetID, Before: a.Before, After: a.After, Actor: Actor{ID: a.ActorID, Name: row.ActorName}, At: a.At.Time})
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
