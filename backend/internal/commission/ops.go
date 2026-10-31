package commission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明提成方案的保存、版本列表和查询操作。
func Register(r *ops.Registry) {
	registerCommissions(r)
	ops.Register(r, ops.Operation[SaveCommissionSchemeInput, CommissionSchemeVersionView]{Name: "save_commission_scheme", Summary: "保存提成方案的新版本", Kind: ops.Write, Permission: perm.SettingsManage, AI: ops.AIDraft, Handle: save})
	ops.Register(r, ops.Operation[ListCommissionSchemeVersionsInput, CommissionSchemeVersionList]{Name: "list_commission_scheme_versions", Summary: "列出提成方案版本", Kind: ops.Read, Permission: perm.SettingsManage, AI: ops.AIDirect, Handle: list})
	ops.Register(r, ops.Operation[GetCommissionSchemeInput, CommissionSchemeVersionView]{Name: "get_commission_scheme", Summary: "按版本号或营业日查看提成方案", Kind: ops.Read, Permission: perm.SettingsManage, AI: ops.AIDirect, Handle: get})
}

// VersionInForce 选择营业日执行且可选不晚于指定保存时刻的方案版本，供业绩解释复用。
func VersionInForce(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, day time.Time, savedNoLaterThan *time.Time) (dbgen.CommissionSchemeVersion, error) {
	rows, err := q.AllCommissionSchemeVersions(ctx, storeID)
	if err != nil {
		return dbgen.CommissionSchemeVersion{}, err
	}
	return selectVersion(rows, day, savedNoLaterThan)
}

func save(ctx context.Context, env ops.Env, in *SaveCommissionSchemeInput) (*CommissionSchemeVersionView, error) {
	from, err := time.Parse(settings.DateLayout, in.EffectiveFrom)
	if err != nil {
		// rule-untested: SaveCommissionSchemeInput.EffectiveFrom 的 schema format=date 已在调用前校验
		return nil, ops.Rule("生效日期的格式是 YYYY-MM-DD")
	}
	if err := env.Q.LockCommissionScheme(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, time.Now())
	if err != nil {
		return nil, err
	}
	if from.Before(today) {
		return nil, ops.Rule("提成方案生效日期不能早于今天（" + today.Format(settings.DateLayout) + "）")
	}
	latest, err := env.Q.LatestCommissionScheme(ctx, env.Caller.StoreID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var before *CommissionSchemeVersionView
	version := int32(1)
	if err == nil {
		if from.Before(latest.EffectiveFrom.Time) {
			return nil, ops.Rule("提成方案生效日期不能早于最新版本（" + latest.EffectiveFrom.Time.Format(settings.DateLayout) + "）")
		}
		version = latest.Version + 1
		before, err = view(ctx, env.Q, latest)
		if err != nil {
			return nil, err
		}
	}
	if err := validate(&in.Scheme); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in.Scheme)
	if err != nil {
		return nil, err
	}
	// 先批量校验引用，再插入版本；引用无 DELETE 权限且允许所有状态，无需行锁。
	proposed := dbgen.CommissionSchemeVersion{StoreID: env.Caller.StoreID, SavedBy: env.Caller.StaffID, Scheme: raw}
	after, err := view(ctx, env.Q, proposed)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.AddCommissionScheme(ctx, dbgen.AddCommissionSchemeParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Version: version, EffectiveFrom: pgtype.Date{Time: from, Valid: true}, Scheme: raw, SavedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	after.ID = row.ID
	after.Version = row.Version
	after.EffectiveFrom = row.EffectiveFrom.Time.Format(settings.DateLayout)
	after.SavedAt = row.SavedAt.Time
	var old any
	if before != nil {
		old = before
	}
	if err := audit.Record(ctx, env, "save_commission_scheme", audit.CommissionSchemeTarget(row.ID), old, after); err != nil {
		return nil, err
	}
	return after, nil
}

func list(ctx context.Context, env ops.Env, in *ListCommissionSchemeVersionsInput) (*CommissionSchemeVersionList, error) {
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListCommissionSchemeVersions(ctx, dbgen.ListCommissionSchemeVersionsParams{StoreID: env.Caller.StoreID, BeforeVersion: in.BeforeVersion, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := &CommissionSchemeVersionList{Versions: []CommissionSchemeVersionSummary{}}
	for _, r := range rows {
		out.Versions = append(out.Versions, CommissionSchemeVersionSummary{ID: r.ID, Version: r.Version, EffectiveFrom: r.EffectiveFrom.Time.Format(settings.DateLayout), SavedBy: staff.Ref{ID: r.SavedBy, Name: r.SavedByName}, SavedAt: r.SavedAt.Time})
	}
	return out, nil
}

func get(ctx context.Context, env ops.Env, in *GetCommissionSchemeInput) (*CommissionSchemeVersionView, error) {
	if (in.Version == nil) == (in.Date == nil) {
		return nil, ops.Rule("请且只按版本号或营业日之一查询提成方案")
	}
	var row dbgen.CommissionSchemeVersion
	var err error
	if in.Version != nil {
		row, err = env.Q.GetCommissionSchemeVersion(ctx, dbgen.GetCommissionSchemeVersionParams{StoreID: env.Caller.StoreID, Version: *in.Version})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("没有这个提成方案版本：%w", ops.ErrNotFound)
		}
	} else {
		day, parseErr := time.Parse(settings.DateLayout, *in.Date)
		if parseErr != nil {
			// rule-untested: GetCommissionSchemeInput.Date 的 schema format=date 已在调用前校验
			return nil, ops.Rule("营业日的格式是 YYYY-MM-DD")
		}
		row, err = VersionInForce(ctx, env.Q, env.Caller.StoreID, day, nil)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("该营业日没有执行的提成方案：%w", ops.ErrNotFound)
		}
	}
	if err != nil {
		return nil, err
	}
	return view(ctx, env.Q, row)
}
