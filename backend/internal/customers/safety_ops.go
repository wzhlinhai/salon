package customers

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// AddSafetyLabelInput 是记录安全提醒的参数。
type AddSafetyLabelInput struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Kind       string    `json:"kind" enum:"allergy,scalp_sensitive,recent_chemical"`
	Source     string    `json:"source" enum:"customer_said,staff_observed"`
	Note       *string   `json:"note,omitempty"`
	ObservedOn string    `json:"observed_on" format:"date"`
}

// RevokeSafetyLabelInput 指定要撤销的安全提醒。
type RevokeSafetyLabelInput struct {
	CustomerID uuid.UUID `json:"customer_id"`
	ID         uuid.UUID `json:"id"`
}

func registerSafety(r *ops.Registry) {
	ops.Register(r, ops.Operation[AddSafetyLabelInput, Customer]{
		Name: "add_safety_label", Summary: "记录顾客安全提醒及观察营业日", Kind: ops.Write,
		Permission: perm.CustomerSafetyLabel, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *AddSafetyLabelInput) (*Customer, error) {
			return changeOne(ctx, env, "add_safety_label", in.CustomerID, func(row *dbgen.Customer) error {
				day, err := time.Parse(time.DateOnly, in.ObservedOn)
				if err != nil {
					// rule-untested: AddSafetyLabelInput.ObservedOn 的 schema format=date 在调用前用 time.DateOnly 校验
					return ops.Rule("观察日期格式不正确")
				}
				today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
				if err != nil {
					return err
				}
				if day.After(today) {
					return ops.Rule("观察日期不能晚于今天的营业日")
				}
				if err := env.Q.AddSafetyLabel(ctx, dbgen.AddSafetyLabelParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CustomerID: row.ID, Kind: in.Kind, Source: in.Source, Note: in.Note, ObservedOn: pgtype.Date{Time: day, Valid: true}, RecordedBy: env.Caller.StaffID}); err != nil {
					return err
				}
				return nil
			})
		},
	})
	ops.Register(r, ops.Operation[RevokeSafetyLabelInput, Customer]{
		Name: "revoke_safety_label", Summary: "撤销安全提醒，保留记录", Kind: ops.Write,
		Permission: perm.CustomerSafetyLabel, AI: ops.AIDraft,
		Handle: func(ctx context.Context, env ops.Env, in *RevokeSafetyLabelInput) (*Customer, error) {
			return changeOne(ctx, env, "revoke_safety_label", in.CustomerID, func(row *dbgen.Customer) error {
				n, err := env.Q.RevokeSafetyLabel(ctx, dbgen.RevokeSafetyLabelParams{ID: in.ID, CustomerID: row.ID, RevokedBy: &env.Caller.StaffID})
				if err != nil {
					return err
				}
				if n == 0 {
					return ops.Rule("安全标签不存在或已撤销")
				}
				return nil
			})
		},
	})
}
