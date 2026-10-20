// Package audit 记录员工与角色的改前改后，并提供查询操作。
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
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

// Entry 是一次操作的记录，改前改后保留原样 JSON。
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
	TargetID *uuid.UUID `json:"target_id,omitempty" doc:"只看这个角色或员工的修改，不填为全部"`
}

// LogList 是 list_audit_log 的返回值，最新的操作在前。
type LogList struct {
	Entries []Entry `json:"entries"`
}

// Register 声明操作日志的读操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListAuditLogInput, LogList]{
		Name: "list_audit_log", Summary: "查看员工与角色的修改记录：谁、何时、改了什么、改前改后", Kind: ops.Read,
		Permission: perm.AuditLogView, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, in *ListAuditLogInput) (*LogList, error) {
			rows, err := env.Q.ListAuditLog(ctx, dbgen.ListAuditLogParams{StoreID: env.Caller.StoreID, TargetID: in.TargetID})
			if err != nil {
				return nil, err
			}
			out := &LogList{Entries: make([]Entry, 0, len(rows))}
			for _, row := range rows {
				a := row.AuditLog
				out.Entries = append(out.Entries, Entry{ID: a.ID, Action: a.Action, TargetID: a.TargetID, Before: a.Before, After: a.After, Actor: Actor{ID: a.ActorID, Name: row.ActorName}, At: a.At.Time})
			}
			return out, nil
		},
	})
}
