// Package storeai 实现门店 AI 的 always-on 骨架：收件箱、事件、会话、定时器、
// 记忆、发件箱、交办、转交和运行（ADR-0015）。投递与存储只传结构化字段；
// 运行由可注入的 RunFunc 提供，生产实现是 Runner。
package storeai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// 收件箱事件的种类。
const (
	// EventFollowUpDue 是跟进或周期定时器到点时写入的事件。
	EventFollowUpDue = "follow_up_due"
	// EventPeriodicDue 是周期定时器到点时写入的事件。
	EventPeriodicDue = "periodic_due"
	// EventRequest 是门店交给员工的一件事。
	EventRequest = "request"
	// EventRequestAnswered 是员工对交办的回答。
	EventRequestAnswered = "request_answered"
	// EventLateAnswer 是改交以后原来那位员工晚到的回答。
	EventLateAnswer = "late_answer"
	// EventCouponRedeemed 是团购券核销成功。
	EventCouponRedeemed = "coupon_redeemed"
	// EventCouponLinked 是团购券关联到单据。
	EventCouponLinked = "coupon_linked"
	// EventCouponUnlinked 是团购券取消关联。
	EventCouponUnlinked = "coupon_unlinked"
	// EventCouponRevoked 是团购核销被撤销。
	EventCouponRevoked = "coupon_revoked"
)

// SubjectCouponRedemption 是团购核销记录的对象类别。
const SubjectCouponRedemption = "coupon_redemption"

// 包内哨兵错误。运行和操作把这些错误映射成自己的响应，不把内部文字直接暴露给模型。
var (
	// ErrInvalidEvent：事件缺少 kind/source_key，或对象标识、payload 不合法。
	ErrInvalidEvent = errors.New("invalid event")
	// ErrInvalidPayload：payload 不是 JSON 对象。
	ErrInvalidPayload = errors.New("payload must be a JSON object")
	// ErrInvalidMemoryPath：AI 记忆的路径不在允许的几种里。
	ErrInvalidMemoryPath = errors.New("invalid memory path")
	// ErrMemoryTooLarge：AI 记忆文件超过 16384 字节。
	ErrMemoryTooLarge = errors.New("memory content exceeds 16384 bytes")
	// ErrMemoryLimit：一个收件箱的 AI 记忆文件已有 200 个。
	ErrMemoryLimit = errors.New("memory file limit reached")
	// ErrFollowUpDue：跟进时间不晚于现在。
	ErrFollowUpDue = errors.New("follow-up time must be after now")
	// ErrInvalidPeriodic：周期定时器的 key 或 repeat 不合法。
	ErrInvalidPeriodic = errors.New("invalid periodic timer")
	// ErrRecipientNotFound：收件人不是本店在职员工。
	ErrRecipientNotFound = errors.New("recipient is not an active staff member of this store")
	// ErrInvalidReminder：AI 提醒的档位、对象或正文不合法。
	ErrInvalidReminder = errors.New("invalid reminder")
	// ErrInvalidRequest：交办的种类或可选答案不合法。
	ErrInvalidRequest = errors.New("invalid request")
	// ErrRequestNotFound：交办不存在，或不是交给这位员工的。
	ErrRequestNotFound = errors.New("request not found")
	// ErrInvalidAnswer：回答不在交办给出的可选答案里。
	ErrInvalidAnswer = errors.New("invalid answer")
	// ErrRequestClosed：交办已经回答、改交或取消。
	ErrRequestClosed = errors.New("request is closed")
	// ErrNoHandoverTarget：原收件人不在职，且没有可改交的在职店长或老板。
	ErrNoHandoverTarget = errors.New("no active handover target")
	// ErrLeaseLost：运行保存时收件箱租约已经不在自己手上。
	ErrLeaseLost = errors.New("inbox lease lost")
	// ErrInvalidRunResult：RunFunc 返回了不认识的会话状态。
	ErrInvalidRunResult = errors.New("invalid run result")
)

// Subject 是「事」的结构化对象标识：类别加编号，两者同时为空或同时有值。
type Subject struct {
	Kind string
	ID   uuid.UUID
}

// Empty 报告对象标识是否为空。
func (s Subject) Empty() bool { return s.Kind == "" && s.ID == uuid.Nil }

// Valid 报告对象标识是否同时有类别和编号，或同时为空。
func (s Subject) Valid() bool { return (s.Kind == "") == (s.ID == uuid.Nil) }

// EmitInput 是写一条收件箱事件所需的字段。StoreID 和 TenantID 与 InboxID 一起定位收件箱。
type EmitInput struct {
	TenantID  uuid.UUID
	StoreID   uuid.UUID
	InboxID   uuid.UUID
	Kind      string
	SourceKey string
	Subject   Subject
	Payload   json.RawMessage
	// NotBefore 为空时用 now。
	NotBefore *time.Time
}

// Emit 是写收件箱事件的唯一入口。同一收件箱里 source_key 重复时不写入、不报错，
// 返回是否新写入。它只做轻量检查，业务检查留给运行。
func Emit(ctx context.Context, q *dbgen.Queries, now time.Time, in EmitInput) (bool, error) {
	if strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.SourceKey) == "" {
		return false, ErrInvalidEvent
	}
	if !in.Subject.Valid() {
		return false, ErrInvalidEvent
	}
	payload, err := objectPayload(in.Payload)
	if err != nil {
		return false, err
	}
	kind, id := subjectColumns(in.Subject)
	notBefore := now
	if in.NotBefore != nil {
		notBefore = *in.NotBefore
	}
	rows, err := q.InsertInboxEvent(ctx, dbgen.InsertInboxEventParams{
		TenantID: in.TenantID, StoreID: in.StoreID, InboxID: in.InboxID,
		Kind: in.Kind, SourceKey: in.SourceKey, SubjectKind: kind, SubjectID: id,
		Payload: payload, NotBefore: timestamp(notBefore), CreatedAt: timestamp(now),
	})
	if err != nil {
		return false, err
	}
	if rows > 0 {
		Wake()
	}
	return rows > 0, nil
}

var wake = make(chan struct{}, 1)

// Wake 非阻塞地叫醒正在 RunLoop 的 worker；事务没提交时 worker 读不到，下一轮轮询照样能取到。
func Wake() {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func subjectColumns(s Subject) (*string, *uuid.UUID) {
	if s.Empty() {
		return nil, nil
	}
	kind, id := s.Kind, s.ID
	return &kind, &id
}

func objectPayload(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, ErrInvalidPayload
	}
	return trimmed, nil
}

func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func optionalTimestamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return timestamp(*t)
}

func timePointer(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func byteLen(text string) int { return len([]byte(text)) }
