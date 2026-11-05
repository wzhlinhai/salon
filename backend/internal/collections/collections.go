package collections

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Collection 是不含顾客资料的收款视图。
type Collection struct {
	ID             uuid.UUID    `json:"id"`
	OrderID        uuid.UUID    `json:"order_id"`
	OrderPaymentID *uuid.UUID   `json:"order_payment_id" nullable:"true"`
	Channel        string       `json:"channel"`
	Amount         money.Amount `json:"amount"`
	Status         string       `json:"status" enum:"pending,processing,succeeded,failed,cancelled,refunded"`
	ExternalRef    *string      `json:"external_ref" nullable:"true"`
	CreatedBy      uuid.UUID    `json:"created_by"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// ListCollectionsInput 指定要查询收款的单据。
type ListCollectionsInput struct {
	OrderID uuid.UUID `json:"order_id"`
}

// GetCollectionInput 指定收款记录。
type GetCollectionInput struct {
	ID uuid.UUID `json:"id"`
}

// ChangeCollectionInput 是撤销或退款的收款和原因。
type ChangeCollectionInput struct {
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason,omitempty"`
}

// CollectionList 是单据的全部收款，按登记顺序返回。
type CollectionList struct {
	Items []Collection `json:"items"`
}

// Register 声明收款查询、撤销和退款操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[ListCollectionsInput, CollectionList]{Name: "list_collections", Summary: "查看单据收款", Kind: ops.Read, Permission: perm.PaymentCollect, AI: ops.AIDirect, Handle: list})
	ops.Register(r, ops.Operation[GetCollectionInput, Collection]{Name: "get_collection", Summary: "查看一笔收款", Kind: ops.Read, Permission: perm.PaymentCollect, AI: ops.AIDirect, Handle: get})
	ops.Register(r, ops.Operation[ChangeCollectionInput, Collection]{Name: "cancel_collection", Summary: "登记撤销收款", Kind: ops.Write, Permission: perm.PaymentCollect, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, in *ChangeCollectionInput) (*Collection, error) {
		return change(ctx, env, in, "cancelled")
	}})
	ops.Register(r, ops.Operation[ChangeCollectionInput, Collection]{Name: "refund_collection", Summary: "登记收款退款", Kind: ops.Write, Permission: perm.PaymentCollect, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, in *ChangeCollectionInput) (*Collection, error) {
		return change(ctx, env, in, "refunded")
	}})
}

func view(row dbgen.Collection) *Collection {
	return &Collection{ID: row.ID, OrderID: row.OrderID, OrderPaymentID: row.OrderPaymentID, Channel: row.Channel, Amount: row.Amount, Status: row.Status, ExternalRef: row.ExternalRef, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	return err
}
func list(ctx context.Context, env ops.Env, in *ListCollectionsInput) (*CollectionList, error) {
	rows, err := env.Q.ListCollections(ctx, dbgen.ListCollectionsParams{StoreID: env.Caller.StoreID, OrderID: in.OrderID})
	if err != nil {
		return nil, err
	}
	out := &CollectionList{Items: make([]Collection, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, *view(row))
	}
	return out, nil
}
func get(ctx context.Context, env ops.Env, in *GetCollectionInput) (*Collection, error) {
	row, err := env.Q.GetCollection(ctx, dbgen.GetCollectionParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	return view(row), nil
}

// Initiate 在调用者事务内发起一笔关联单据的收款，启停取收款时的营业日设置。
func Initiate(ctx context.Context, env ops.Env, code string, orderID uuid.UUID, paymentID *uuid.UUID, amount money.Amount, externalRef *string) (*Collection, error) {
	r, err := Lookup(code)
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
	if !rules.CollectionChannelEnabled(code) {
		return nil, ops.Rule("收款通道已停用：" + r.Name)
	}
	row, err := env.Q.CreateCollection(ctx, dbgen.CreateCollectionParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, OrderID: orderID, OrderPaymentID: paymentID, Channel: code, Amount: amount, ExternalRef: externalRef, CreatedBy: env.Caller.StaffID, CreatedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}})
	if err != nil {
		return nil, err
	}
	result, err := r.Channel.Initiate(ctx, env, row)
	if err != nil {
		return nil, err
	}
	return apply(ctx, env, row, result, "发起收款", "initiate_collection", nil)
}

// Query 调用已登记收款的通道查询接口，不写入状态，供通道同步流程使用。
func Query(ctx context.Context, env ops.Env, id uuid.UUID) (Result, error) {
	row, err := env.Q.GetCollection(ctx, dbgen.GetCollectionParams{StoreID: env.Caller.StoreID, ID: id})
	if err != nil {
		return Result{}, rowError(err)
	}
	r, err := Lookup(row.Channel)
	if err != nil {
		return Result{}, err
	}
	return r.Channel.Query(ctx, env, row)
}

func change(ctx context.Context, env ops.Env, in *ChangeCollectionInput, to string) (*Collection, error) {
	reason := strings.TrimSpace(in.Reason)
	if to == "refunded" && reason == "" {
		return nil, ops.Rule("请填写退款原因")
	}
	row, err := env.Q.LockCollection(ctx, dbgen.LockCollectionParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, rowError(err)
	}
	r, err := Lookup(row.Channel)
	if err != nil {
		return nil, err
	}
	action := "cancel_collection"
	effect := func() error { return r.Channel.Cancel(ctx, env, row) }
	if to == "refunded" {
		action = "refund_collection"
		effect = func() error { return r.Channel.Refund(ctx, env, row) }
	}

	return apply(ctx, env, row, Result{Status: to, ExternalRef: row.ExternalRef}, reason, action, effect)
}

func apply(ctx context.Context, env ops.Env, row dbgen.Collection, result Result, reason, action string, effect func() error) (*Collection, error) {
	if err := Transition(row.Status, result.Status); err != nil {
		return nil, err
	}
	if effect != nil {
		if err := effect(); err != nil {
			return nil, err
		}
	}
	updated, err := env.Q.UpdateCollection(ctx, dbgen.UpdateCollectionParams{StoreID: env.Caller.StoreID, ID: row.ID, Status: result.Status, UpdatedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}, ExternalRef: result.ExternalRef})
	if err != nil {
		return nil, err
	}
	if err := env.Q.AddCollectionEvent(ctx, dbgen.AddCollectionEventParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CollectionID: row.ID, FromStatus: row.Status, ToStatus: result.Status, ActorID: env.Caller.StaffID, At: pgtype.Timestamptz{Time: env.Now, Valid: true}, Reason: reason}); err != nil {
		return nil, err
	}
	out := view(updated)
	var before any
	if action != "initiate_collection" {
		before = view(row)
	}
	if err := audit.Record(ctx, env, action, audit.CollectionTarget(row.ID), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

// CancelOrder 撤销该单的所有收款；调用者已持单锁，逐笔按登记顺序锁收款。
func CancelOrder(ctx context.Context, env ops.Env, orderID uuid.UUID) error {
	rows, err := env.Q.ListCollections(ctx, dbgen.ListCollectionsParams{StoreID: env.Caller.StoreID, OrderID: orderID})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := change(ctx, env, &ChangeCollectionInput{ID: row.ID, Reason: "撤单"}, "cancelled"); err != nil {
			return err
		}
	}
	return nil
}

func reconcile(ctx context.Context, env ops.Env, code string, day time.Time) (Summary, error) {
	rows, err := env.Q.ReconcileCollections(ctx, dbgen.ReconcileCollectionsParams{StoreID: env.Caller.StoreID, Channel: code})
	if err != nil {
		return Summary{}, err
	}
	times := make([]time.Time, len(rows))
	for i, row := range rows {
		times[i] = row.CreatedAt.Time
	}
	days, err := settings.BusinessDates(ctx, env.Q, env.Caller.StoreID, times)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{BusinessDate: day.Format(time.DateOnly), Channel: code, Amounts: map[string]string{}, Counts: map[string]int{}}
	totals := map[string]money.Total{}
	for i, row := range rows {
		if days[i].Format(time.DateOnly) == out.BusinessDate {
			totals[row.Status] = totals[row.Status].AddProduct(row.Amount, 1)
			out.Counts[row.Status]++
		}
	}
	for status, total := range totals {
		out.Amounts[status] = total.Format()
	}
	return out, nil
}
