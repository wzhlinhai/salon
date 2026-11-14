// Package drafts 保存 AI 草稿，由人确认后在同一事务执行目标操作。
package drafts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Draft 是草稿的当前内容；原始参数始终保留。
type Draft struct {
	ID             uuid.UUID       `json:"id"`
	Initiator      uuid.UUID       `json:"initiator"`
	Operation      string          `json:"operation"`
	Utterance      string          `json:"utterance"`
	Params         json.RawMessage `json:"params"`
	OriginalParams json.RawMessage `json:"original_params"`
	Summary        string          `json:"summary"`
	Status         string          `json:"status"`
	BusinessDate   string          `json:"business_date"`
	ExpiresAt      time.Time       `json:"expires_at"`
	CreatedAt      time.Time       `json:"created_at"`
	DecidedAt      *time.Time      `json:"decided_at"`
	DecidedBy      *uuid.UUID      `json:"decided_by"`
	RejectReason   *string         `json:"reject_reason"`
}

// Event 是一次只追加的草稿变更。
type Event struct {
	ID           uuid.UUID       `json:"id"`
	Kind         string          `json:"kind"`
	BeforeParams json.RawMessage `json:"before_params"`
	AfterParams  json.RawMessage `json:"after_params"`
	ActorID      uuid.UUID       `json:"actor_id"`
	At           time.Time       `json:"at"`
}

// Detail 包含草稿、完整变更历史和产生的业务日志标识。
type Detail struct {
	Draft
	Events      []Event     `json:"events"`
	AuditLogIDs []uuid.UUID `json:"audit_log_ids"`
}

// CreateInput 是人工或模型提供的原始起草内容。
type CreateInput struct {
	Operation string         `json:"operation" minLength:"1"`
	Params    map[string]any `json:"params"`
	Utterance string         `json:"utterance"`
	Summary   string         `json:"summary"`
}

// IDInput 指定一张草稿。
type IDInput struct {
	ID uuid.UUID `json:"id"`
}

// UpdateInput 是确认前的人工作业参数。
type UpdateInput struct {
	ID     uuid.UUID      `json:"id"`
	Params map[string]any `json:"params"`
}

// RejectInput 保存拒绝原因。
type RejectInput struct {
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason" minLength:"1"`
}

// List 是可见草稿列表，按创建时刻和 ID 升序返回。
type List struct {
	Drafts []Draft `json:"drafts"`
}

type service struct{ registry *ops.Registry }

// Register 注册草稿操作并接入 AI 写操作的起草路径；确认仅向人开放。
func Register(r *ops.Registry) {
	s := service{registry: r}
	r.SetDraftCreator(s.create)
	ops.Register(r, ops.Operation[CreateInput, Draft]{Name: "create_draft", Summary: "创建待确认草稿", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AIManageDraft, Handle: func(ctx context.Context, env ops.Env, in *CreateInput) (*Draft, error) {
		params, err := json.Marshal(in.Params)
		if err != nil {
			return nil, err
		}
		out, err := s.create(ctx, env, in.Operation, params, in.Utterance, in.Summary)
		if err != nil {
			return nil, err
		}
		return out.(*Draft), nil
	}})
	ops.Register(r, ops.Operation[struct{}, List]{Name: "list_drafts", Summary: "查看可见草稿", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect, Handle: s.list})
	ops.Register(r, ops.Operation[IDInput, Detail]{Name: "get_draft", Summary: "查看草稿与留痕", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect, Handle: s.get})
	ops.Register(r, ops.Operation[UpdateInput, Draft]{Name: "update_draft_params", Summary: "修改待确认草稿参数", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AIManageDraft, Handle: s.update})
	ops.Register(r, ops.Operation[IDInput, Draft]{Name: "confirm_draft", Summary: "由人复检权限并确认执行草稿", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AINone, Handle: s.confirm})
	ops.Register(r, ops.Operation[RejectInput, Draft]{Name: "reject_draft", Summary: "拒绝草稿并保存原因", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AIManageDraft, Handle: s.reject})
}

func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func view(env ops.Env, d dbgen.AiDraft) (*Draft, error) {
	out := &Draft{ID: d.ID, Initiator: d.Initiator, Operation: d.Operation, Utterance: d.Utterance, Params: d.Params, OriginalParams: d.OriginalParams, Summary: d.Summary, Status: d.Status, BusinessDate: d.BusinessDate.Time.Format(time.DateOnly), ExpiresAt: d.ExpiresAt.Time, CreatedAt: d.CreatedAt.Time, DecidedBy: d.DecidedBy, RejectReason: d.RejectReason}
	if out.Status == "pending" && !env.Now.Before(d.ExpiresAt.Time) {
		out.Status = "expired"
	}
	if d.DecidedAt.Valid {
		out.DecidedAt = &d.DecidedAt.Time
	}
	var err error
	out.Params, err = redact(env, out.Params)
	if err != nil {
		return nil, err
	}
	out.OriginalParams, err = redact(env, out.OriginalParams)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func redact(env ops.Env, raw json.RawMessage) (json.RawMessage, error) {
	if env.Caller.Has(perm.CustomerFullPhone) {
		return raw, nil
	}
	return audit.MaskPhones(raw)
}

func (s service) create(ctx context.Context, env ops.Env, name string, params json.RawMessage, utterance, summary string) (any, error) {
	if err := s.registry.CheckDraft(env.Caller, name, params); err != nil {
		return nil, err
	}
	if err := env.Q.ExpireStoreDrafts(ctx, dbgen.ExpireStoreDraftsParams{StoreID: env.Caller.StoreID, Now: stamp(env.Now), ActorID: env.Caller.StaffID}); err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	deadline, err := settings.BusinessDayDeadline(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	d, err := env.Q.CreateDraft(ctx, dbgen.CreateDraftParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Initiator: env.Caller.StaffID, Operation: name, Utterance: utterance, Params: params, Summary: summary, BusinessDate: pgtype.Date{Time: day, Valid: true}, ExpiresAt: stamp(deadline), CreatedAt: stamp(env.Now)})
	if err != nil {
		return nil, err
	}
	if err := event(ctx, env, d, "created", nil); err != nil {
		return nil, err
	}
	return view(env, d)
}

func event(ctx context.Context, env ops.Env, d dbgen.AiDraft, kind string, before []byte) error {
	return env.Q.AddDraftEvent(ctx, dbgen.AddDraftEventParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, DraftID: d.ID, Kind: kind, BeforeParams: before, AfterParams: d.Params, ActorID: env.Caller.StaffID, At: stamp(env.Now)})
}

func save(ctx context.Context, env ops.Env, d dbgen.AiDraft) (dbgen.AiDraft, error) {
	return env.Q.SaveDraft(ctx, dbgen.SaveDraftParams{StoreID: env.Caller.StoreID, ID: d.ID, Params: d.Params, Status: d.Status, DecidedAt: d.DecidedAt, DecidedBy: d.DecidedBy, RejectReason: d.RejectReason})
}

func (s service) visible(env ops.Env, d dbgen.AiDraft) bool {
	p, err := s.registry.DraftPermission(d.Operation)
	return d.Initiator == env.Caller.StaffID || (err == nil && env.Caller.Has(p))
}

// lock 在锁内重读并持久化过期；不可见目标不泄露内容。
func (s service) lock(ctx context.Context, env ops.Env, id uuid.UUID, requireVisible bool) (dbgen.AiDraft, error) {
	d, err := env.Q.LockDraft(ctx, dbgen.LockDraftParams{StoreID: env.Caller.StoreID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ops.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if requireVisible && !s.visible(env, d) {
		return d, ops.ErrNotFound
	}
	return s.persistExpiry(ctx, env, d)
}

func (s service) persistExpiry(ctx context.Context, env ops.Env, d dbgen.AiDraft) (dbgen.AiDraft, error) {
	var err error
	if d.Status == "pending" && !env.Now.Before(d.ExpiresAt.Time) {
		d.Status = "expired"
		d.DecidedAt = stamp(env.Now)
		d, err = save(ctx, env, d)
		if err != nil {
			return d, err
		}
		err = event(ctx, env, d, "expired", d.Params)
	}
	return d, err
}

func pending(d dbgen.AiDraft) error {
	if d.Status == "expired" {
		return ops.CommitRule("草稿已过期，请重新起草")
	}
	if d.Status != "pending" {
		return ops.Rule("草稿不是待确认状态")
	}
	return nil
}

func (s service) list(ctx context.Context, env ops.Env, _ *struct{}) (*List, error) {
	rows, err := env.Q.ListDrafts(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &List{Drafts: []Draft{}}
	for _, d := range rows {
		if !s.visible(env, d) {
			continue
		}
		item, err := view(env, d)
		if err != nil {
			return nil, err
		}
		out.Drafts = append(out.Drafts, *item)
	}
	return out, nil
}

func (s service) get(ctx context.Context, env ops.Env, in *IDInput) (*Detail, error) {
	d, err := env.Q.GetDraft(ctx, dbgen.GetDraftParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.visible(env, d) {
		return nil, ops.ErrNotFound
	}
	rows, err := env.Q.DraftEvents(ctx, dbgen.DraftEventsParams{StoreID: env.Caller.StoreID, DraftID: d.ID})
	if err != nil {
		return nil, err
	}
	ids, err := env.Q.DraftAuditIDs(ctx, dbgen.DraftAuditIDsParams{StoreID: env.Caller.StoreID, DraftID: &d.ID})
	if err != nil {
		return nil, err
	}
	item, err := view(env, d)
	if err != nil {
		return nil, err
	}
	out := &Detail{Draft: *item, Events: []Event{}, AuditLogIDs: append([]uuid.UUID{}, ids...)}
	for _, e := range rows {
		e.BeforeParams, err = redact(env, e.BeforeParams)
		if err != nil {
			return nil, err
		}
		e.AfterParams, err = redact(env, e.AfterParams)
		if err != nil {
			return nil, err
		}
		out.Events = append(out.Events, Event{ID: e.ID, Kind: e.Kind, BeforeParams: e.BeforeParams, AfterParams: e.AfterParams, ActorID: e.ActorID, At: e.At.Time})
	}
	return out, nil
}

func (s service) update(ctx context.Context, env ops.Env, in *UpdateInput) (*Draft, error) {
	d, err := s.lock(ctx, env, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := pending(d); err != nil {
		return nil, err
	}
	params, err := json.Marshal(in.Params)
	if err != nil {
		return nil, err
	}
	// 发起人可能已失去权限，仍可整理参数；执行权限在确认时复检。
	if err := s.registry.ValidateDraftParams(d.Operation, params); err != nil {
		return nil, err
	}
	before := d.Params
	d.Params = params
	d, err = save(ctx, env, d)
	if err != nil {
		return nil, err
	}
	if err := event(ctx, env, d, "modified", before); err != nil {
		return nil, err
	}
	return view(env, d)
}

// 金额类白名单统一在这里声明。
var monetary = map[string]bool{"adjust_card": true, "void_order": true, "refund_card": true, "reprice_order_item": true}

func (s service) confirm(ctx context.Context, env ops.Env, in *IDInput) (*Draft, error) {
	d, err := s.lock(ctx, env, in.ID, false)
	if err != nil {
		return nil, err
	}
	if err := pending(d); err != nil {
		return nil, err
	}
	execute, err := s.registry.PrepareDraft(env.Caller, d.Operation, d.Params)
	if err != nil {
		return nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	if monetary[d.Operation] && rules.DraftDualConfirm() && d.Initiator == env.Caller.StaffID {
		return nil, ops.Rule("金额草稿必须由另一人确认")
	}
	env.DraftID = &d.ID
	if _, err := execute(ctx, env); err != nil {
		return nil, err
	}
	events, err := env.Q.DraftEvents(ctx, dbgen.DraftEventsParams{StoreID: env.Caller.StoreID, DraftID: d.ID})
	if err != nil {
		return nil, err
	}
	d.Status = "confirmed"
	for _, e := range events {
		if e.Kind == "modified" {
			d.Status = "confirmed_modified"
			break
		}
	}
	d.DecidedAt = stamp(env.Now)
	d.DecidedBy = &env.Caller.StaffID
	d, err = save(ctx, env, d)
	if err != nil {
		return nil, err
	}
	if err := event(ctx, env, d, "confirmed", d.Params); err != nil {
		return nil, err
	}
	return view(env, d)
}

func (s service) reject(ctx context.Context, env ops.Env, in *RejectInput) (*Draft, error) {
	d, err := s.lock(ctx, env, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := pending(d); err != nil {
		return nil, err
	}
	d.Status = "rejected"
	d.DecidedAt = stamp(env.Now)
	d.DecidedBy = &env.Caller.StaffID
	d.RejectReason = &in.Reason
	d, err = save(ctx, env, d)
	if err != nil {
		return nil, err
	}
	if err := event(ctx, env, d, "rejected", d.Params); err != nil {
		return nil, err
	}
	return view(env, d)
}
