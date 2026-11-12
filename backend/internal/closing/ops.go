package closing

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/closingguard"
	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Register 声明日结、封账和撤销封账操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[DayInput, SystemFigures]{Name: "preview_day_close", Summary: "预览一个营业日的系统应收和对账汇总", Kind: ops.Read, Permission: perm.ClosingDaily, AI: ops.AIDirect, Handle: previewDayClose})
	ops.Register(r, ops.Operation[CloseDayInput, DayClose]{Name: "close_day", Summary: "日结并保存系统快照与实际到账差异", Kind: ops.Write, Permission: perm.ClosingDaily, AI: ops.AIDraft, Handle: closeDay})
	ops.Register(r, ops.Operation[DayInput, DayCloseDetail]{Name: "get_day_close", Summary: "查看某天的当前日结和历史版本", Kind: ops.Read, Permission: perm.ClosingDaily, AI: ops.AIDirect, Handle: getDayClose})
	ops.Register(r, ops.Operation[ListDayClosesInput, DayCloseList]{Name: "list_day_closes", Summary: "按营业日范围查看日结及封账状态", Kind: ops.Read, Permission: perm.ClosingDaily, AI: ops.AIDirect, Handle: listDayCloses})
	ops.Register(r, ops.Operation[DayInput, SealResult]{Name: "seal_day", Summary: "按日期顺序封账", Kind: ops.Write, Permission: perm.ClosingSeal, AI: ops.AIDraft, Handle: sealDay})
	ops.Register(r, ops.Operation[UnsealDayInput, SealResult]{Name: "unseal_day", Summary: "撤销最后一个封账日并记录原因", Kind: ops.Write, Permission: perm.ClosingUnseal, AI: ops.AIDraft, Handle: unsealDay})
}

// ListDayClosesInput 是日结列表的营业日范围。
type ListDayClosesInput struct {
	DateFrom string `json:"date_from" format:"date"`
	DateTo   string `json:"date_to" format:"date"`
}

func parseDay(s string) (time.Time, error) {
	day, err := time.Parse(settings.DateLayout, s)
	if err != nil {
		// rule-untested: 日期字段由 schema format=date 在调用前校验
		return time.Time{}, ops.Rule("营业日格式无效")
	}
	return day, nil
}

func currentDay(ctx context.Context, env ops.Env) (time.Time, error) {
	return settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
}

func previewDayClose(ctx context.Context, env ops.Env, in *DayInput) (*SystemFigures, error) {
	day, err := parseDay(in.Date)
	if err != nil {
		return nil, err
	}
	return systemFigures(ctx, env, day)
}

func closeDay(ctx context.Context, env ops.Env, in *CloseDayInput) (*DayClose, error) {
	day, err := parseDay(in.Date)
	if err != nil {
		return nil, err
	}
	if err := env.Q.LockClosing(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	today, err := currentDay(ctx, env)
	if err != nil {
		return nil, err
	}
	if !day.Before(today) {
		return nil, ops.Rule("只能日结今天以前的营业日")
	}
	sealed, err := closingguard.SealedThrough(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	if sealed != nil && !day.After(*sealed) {
		return nil, ops.Rule("该营业日已封账（" + day.Format(settings.DateLayout) + "）")
	}
	system, err := systemFigures(ctx, env, day)
	if err != nil {
		return nil, err
	}
	diffs := differences(system, in)
	systemJSON, err := json.Marshal(system)
	if err != nil {
		return nil, err
	}
	diffJSON, err := json.Marshal(diffs)
	if err != nil {
		return nil, err
	}
	version, err := env.Q.NextDayCloseVersion(ctx, dbgen.NextDayCloseVersionParams{StoreID: env.Caller.StoreID, BusinessDate: pgDate(day)})
	if err != nil {
		return nil, err
	}
	note := trimNote(in.Note)
	row, err := env.Q.AddDayClose(ctx, dbgen.AddDayCloseParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BusinessDate: pgDate(day), Version: version, System: systemJSON, Differences: diffJSON, CashCounted: in.CashCounted, UnionpayActual: in.UnionpayActual, WechatActual: in.WechatActual, AlipayActual: in.AlipayActual, Note: note, CreatedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	out, err := viewDayClose(row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "close_day", audit.DayCloseTarget(row.ID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func trimNote(note *string) *string {
	if note == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*note)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func getDayClose(ctx context.Context, env ops.Env, in *DayInput) (*DayCloseDetail, error) {
	day, err := parseDay(in.Date)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListDayCloseVersions(ctx, dbgen.ListDayCloseVersionsParams{StoreID: env.Caller.StoreID, BusinessDate: pgDate(day)})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ops.ErrNotFound
	}
	versions := make([]DayClose, 0, len(rows))
	for _, row := range rows {
		view, err := viewDayClose(row)
		if err != nil {
			return nil, err
		}
		versions = append(versions, *view)
	}
	return &DayCloseDetail{Current: versions[0], Versions: versions}, nil
}

func listDayCloses(ctx context.Context, env ops.Env, in *ListDayClosesInput) (*DayCloseList, error) {
	query, err := daterange.Parse(env.Caller, perm.ClosingDaily, in.DateFrom, in.DateTo, nil, 0)
	if err != nil {
		return nil, err
	}
	sealed, err := closingguard.SealedThrough(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListCurrentDayCloses(ctx, dbgen.ListCurrentDayClosesParams{StoreID: env.Caller.StoreID, DateFrom: pgDate(query.From), DateTo: pgDate(query.To)})
	if err != nil {
		return nil, err
	}
	out := &DayCloseList{SealedThrough: dateValue(sealed), Items: []DayCloseListItem{}}
	for _, row := range rows {
		day := row.BusinessDate.Time
		out.Items = append(out.Items, DayCloseListItem{ID: row.ID, BusinessDate: day.Format(settings.DateLayout), Version: row.Version, CashCounted: row.CashCounted, UnionpayActual: row.UnionpayActual, WechatActual: row.WechatActual, AlipayActual: row.AlipayActual, Note: row.Note, Sealed: sealed != nil && !day.After(*sealed), CreatedAt: row.CreatedAt.Time})
	}
	return out, nil
}

func sealDay(ctx context.Context, env ops.Env, in *DayInput) (*SealResult, error) {
	day, err := parseDay(in.Date)
	if err != nil {
		return nil, err
	}
	if err := env.Q.LockClosing(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	today, err := currentDay(ctx, env)
	if err != nil {
		return nil, err
	}
	if !day.Before(today) {
		return nil, ops.Rule("只能封今天以前的营业日")
	}
	sealed, err := closingguard.SealedThrough(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	if sealed != nil && !day.Equal(sealed.AddDate(0, 0, 1)) {
		return nil, ops.Rule("只能按日期顺序封账，下一个封账日是 " + sealed.AddDate(0, 0, 1).Format(settings.DateLayout))
	}
	open, err := env.Q.OpenOrdersThrough(ctx, dbgen.OpenOrdersThroughParams{StoreID: env.Caller.StoreID, ThroughDate: pgDate(day)})
	if err != nil {
		return nil, err
	}
	if len(open) > 0 {
		ids := make([]string, len(open))
		for i, id := range open {
			ids[i] = id.String()
		}
		return nil, ops.Rule("有未结账的挂单，请先处理：" + strings.Join(ids, ", "))
	}
	versions, err := env.Q.ListDayCloseVersions(ctx, dbgen.ListDayCloseVersionsParams{StoreID: env.Caller.StoreID, BusinessDate: pgDate(day)})
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		has, err := hasBusiness(ctx, env, day)
		if err != nil {
			return nil, err
		}
		if has {
			return nil, ops.Rule("请先日结 " + day.Format(settings.DateLayout))
		}
	} else {
		current, err := viewDayClose(versions[0])
		if err != nil {
			return nil, err
		}
		system, err := systemFigures(ctx, env, day)
		if err != nil {
			return nil, err
		}
		storedJSON, err := json.Marshal(current.System)
		if err != nil {
			return nil, err
		}
		currentJSON, err := json.Marshal(system)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(storedJSON, currentJSON) {
			return nil, ops.Rule("日结后当天账目有变化，请重新日结")
		}
	}
	previous := pgtype.Date{}
	if sealed != nil {
		previous = pgDate(*sealed)
	}
	event, err := env.Q.AddClosingEvent(ctx, dbgen.AddClosingEventParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BusinessDate: pgDate(day), PreviousThrough: previous, Kind: "seal", ActorID: env.Caller.StaffID, At: pgtype.Timestamptz{Time: env.Now, Valid: true}})
	if err != nil {
		return nil, err
	}
	if err := env.Q.SetStoreSealing(ctx, dbgen.SetStoreSealingParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, SealedThrough: pgDate(day)}); err != nil {
		return nil, err
	}
	out := &SealResult{SealedThrough: dateValue(&day), EventID: event.ID, BusinessDate: day.Format(settings.DateLayout), Kind: "seal"}
	if err := audit.Record(ctx, env, "seal_day", audit.ClosingTarget(event.ID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func unsealDay(ctx context.Context, env ops.Env, in *UnsealDayInput) (*SealResult, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return nil, ops.Rule("请填写不超过200字的撤销封账原因")
	}
	if err := env.Q.LockClosing(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	sealed, err := closingguard.SealedThrough(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	if sealed == nil {
		return nil, ops.Rule("当前没有封账")
	}
	day := *sealed
	sealEvent, err := env.Q.LatestSealEvent(ctx, dbgen.LatestSealEventParams{StoreID: env.Caller.StoreID, BusinessDate: pgDate(day)})
	if err != nil {
		return nil, err
	}
	previous := sealEvent.PreviousThrough
	next := pgtype.Date{}
	if previous.Valid {
		next = previous
	}
	event, err := env.Q.AddClosingEvent(ctx, dbgen.AddClosingEventParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, BusinessDate: pgDate(day), Kind: "unseal", Reason: &reason, ActorID: env.Caller.StaffID, At: pgtype.Timestamptz{Time: env.Now, Valid: true}})
	if err != nil {
		return nil, err
	}
	if err := env.Q.SetStoreSealing(ctx, dbgen.SetStoreSealingParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, SealedThrough: next}); err != nil {
		return nil, err
	}
	var through *time.Time
	if previous.Valid {
		through = &previous.Time
	}
	out := &SealResult{SealedThrough: dateValue(through), EventID: event.ID, BusinessDate: day.Format(settings.DateLayout), Kind: "unseal"}
	if err := audit.Record(ctx, env, "unseal_day", audit.ClosingTarget(event.ID), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func viewDayClose(row dbgen.DayClose) (*DayClose, error) {
	var system SystemFigures
	if err := json.Unmarshal(row.System, &system); err != nil {
		return nil, err
	}
	var diffs []MethodDifference
	if err := json.Unmarshal(row.Differences, &diffs); err != nil {
		return nil, err
	}
	return &DayClose{ID: row.ID, BusinessDate: row.BusinessDate.Time.Format(settings.DateLayout), Version: row.Version, System: system, Differences: diffs, CashCounted: row.CashCounted, UnionpayActual: row.UnionpayActual, WechatActual: row.WechatActual, AlipayActual: row.AlipayActual, Note: row.Note, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time}, nil
}

func dateValue(day *time.Time) *string {
	if day == nil {
		return nil
	}
	out := day.Format(settings.DateLayout)
	return &out
}
