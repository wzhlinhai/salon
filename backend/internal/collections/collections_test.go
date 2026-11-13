package collections_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/collections"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func call(t *testing.T, s *optest.Shop, name string, in any) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, name, in, 200).Body
}
func order(t *testing.T, s *optest.Shop) uuid.UUID {
	t.Helper()
	return uuid.MustParse(call(t, s, "open_order", map[string]any{"gender": "female", "designated": false, "items": []any{}})["id"].(string))
}
func amount() money.Amount {
	var a money.Amount
	if err := a.Scan("10"); err != nil {
		panic(err)
	}
	return a
}
func initiate(t *testing.T, s *optest.Shop, code string, o uuid.UUID) *collections.Collection {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	env := ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner), Now: s.Now()}
	row, err := collections.Initiate(context.Background(), env, code, o, nil, amount(), new("receipt-42"))
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	return row
}
func snapshot(t *testing.T, s *optest.Shop) string {
	t.Helper()
	return dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "collections", "collection_events", "audit_log")
}
func refuse(t *testing.T, s *optest.Shop, op string, in any, detail string) {
	t.Helper()
	before := snapshot(t, s)
	r := s.HTTPWithToken(t, s.Owner, op, in)
	if r.Status != 409 || r.Detail() != detail || snapshot(t, s) != before {
		t.Fatal(r, "refusal changed data")
	}
}

func TestLifecycle(t *testing.T) {
	s := optest.OpenShop(t)
	now := time.Date(2027, 2, 3, 11, 12, 13, 0, settings.Location)
	s.SetNow(now)
	o := order(t, s)
	for _, code := range []string{"cash", "unionpay_register"} {
		row := initiate(t, s, code, o)
		if row.Status != "succeeded" || row.OrderID != o || row.Amount.String() != "10.00" || !row.CreatedAt.Equal(now) || !row.UpdatedAt.Equal(now) || row.CreatedBy != s.Res.OwnerID || *row.ExternalRef != "receipt-42" {
			t.Fatal(row)
		}
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "get_collection", collections.GetCollectionInput{ID: row.ID})
			if r.Status != 200 || r.Body["status"] != "succeeded" || r.Body["external_ref"] != "receipt-42" {
				t.Fatal(r)
			}
			r = ch.Call(t, s.MustCaller(s.Owner), "list_collections", collections.ListCollectionsInput{OrderID: o})
			if r.Status != 200 || len(r.Body["items"].([]any)) < 1 {
				t.Fatal(r)
			}
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		env := ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner), Now: now}
		result, err := collections.Query(context.Background(), env, row.ID)
		if err != nil || result.Status != "succeeded" {
			t.Fatal(result, err)
		}
		reg, err := collections.Lookup(code)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := reg.Channel.Reconcile(context.Background(), env, settings.Date(now))
		if err != nil || sum.Amounts["succeeded"] != "10.00" || sum.Counts["succeeded"] != 1 || len(sum.Counts) != 1 {
			t.Fatal(sum, err)
		}
		other, err := reg.Channel.Reconcile(context.Background(), env, settings.Date(now).AddDate(0, 0, 1))
		if err != nil || len(other.Counts) != 0 {
			t.Fatal(other, err)
		}
		dbtest.Rollback(t, tx)
		s.SetNow(now.Add(time.Minute))
		out := call(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID, Reason: "登记错误"})
		updatedAt, err := time.Parse(time.RFC3339, out["updated_at"].(string))
		if err != nil || out["status"] != "cancelled" || !updatedAt.Equal(now.Add(time.Minute)) {
			t.Fatal(out)
		}
		refuse(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID}, "收款状态不能转换：cancelled → cancelled")
		refuse(t, s, "refund_collection", collections.ChangeCollectionInput{ID: row.ID, Reason: "退"}, "收款状态不能转换：cancelled → refunded")
		checkHistory(t, s, row.ID, []string{"pending>succeeded:发起收款", "succeeded>cancelled:登记错误"}, []string{"initiate_collection", "cancel_collection"})
		s.SetNow(now)
	}
	row := initiate(t, s, "cash", o)
	refuse(t, s, "refund_collection", collections.ChangeCollectionInput{ID: row.ID, Reason: "  "}, "请填写退款原因")
	s.SetNow(now.Add(time.Minute))
	out := call(t, s, "refund_collection", collections.ChangeCollectionInput{ID: row.ID, Reason: "  退货  "})
	if out["status"] != "refunded" {
		t.Fatal(out)
	}
	refuse(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID}, "收款状态不能转换：refunded → cancelled")
	checkHistory(t, s, row.ID, []string{"pending>succeeded:发起收款", "succeeded>refunded:退货"}, []string{"initiate_collection", "refund_collection"})
}

func checkHistory(t *testing.T, s *optest.Shop, id uuid.UUID, want, actions []string) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), "SELECT from_status||'>'||to_status||':'||reason,actor_id FROM collection_events WHERE collection_id=$1 ORDER BY at,id", id)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for rows.Next() {
		var v string
		var actor uuid.UUID
		if err := rows.Scan(&v, &actor); err != nil {
			t.Fatal(err)
		}
		if actor != s.Res.OwnerID {
			t.Fatal(actor)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	rows, err = tx.Query(context.Background(), "SELECT action,customer_id,before->>'status',after->>'status' FROM audit_log WHERE target_kind='collection' AND target_id=$1 ORDER BY at,id", id)
	if err != nil {
		t.Fatal(err)
	}
	got = []string{}
	for rows.Next() {
		var action, after string
		var before *string
		var customer *uuid.UUID
		if err := rows.Scan(&action, &customer, &before, &after); err != nil {
			t.Fatal(err)
		}
		if customer != nil || (before != nil && *before == after) || (action == "initiate_collection" && before != nil) || (action != "initiate_collection" && before == nil) {
			t.Fatal(customer, before, after)
		}
		got = append(got, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	dbtest.Rollback(t, tx)
	if !reflect.DeepEqual(got, actions) {
		t.Fatal(got, actions)
	}
}

func TestDisabled(t *testing.T) {
	s := optest.OpenShop(t)
	o := order(t, s)
	day := settings.Date(s.Now()).Format(time.DateOnly)
	for _, tc := range []struct{ code, name string }{{"cash", "现金"}, {"unionpay_register", "银联登记"}} {
		row := initiate(t, s, tc.code, o)
		call(t, s, "change_setting", map[string]any{"key": "collection_channel_" + tc.code, "value": "off", "effective_from": day})
		before := snapshot(t, s)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := collections.Initiate(context.Background(), ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner), Now: s.Now()}, tc.code, o, nil, amount(), nil)
		if err == nil || err.Error() != ops.Rule("收款通道已停用："+tc.name).Error() {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
		if snapshot(t, s) != before {
			t.Fatal("disabled wrote data")
		}
		call(t, s, "refund_collection", collections.ChangeCollectionInput{ID: row.ID, Reason: "已停用通道仍可退"})
	}
	_, err := collections.Lookup("missing")
	if err == nil || err.Error() != ops.Rule("未知收款通道：missing").Error() {
		t.Fatal(err)
	}
}

func TestAccess(t *testing.T) {
	s := optest.OpenShop(t)
	row := initiate(t, s, "cash", order(t, s))
	other := order(t, s)
	owner := s.MustCaller(s.Owner)
	cases := []struct {
		op string
		in any
	}{{"list_collections", collections.ListCollectionsInput{OrderID: row.OrderID}}, {"get_collection", collections.GetCollectionInput{ID: row.ID}}, {"cancel_collection", collections.ChangeCollectionInput{ID: row.ID}}, {"refund_collection", collections.ChangeCollectionInput{ID: row.ID, Reason: "退"}}}
	before := snapshot(t, s)
	for _, ch := range s.Channels() {
		r := ch.Call(t, owner, "list_collections", collections.ListCollectionsInput{OrderID: other})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 0 {
			t.Fatal(r)
		}
		for _, tc := range cases {
			caller := s.MustCaller(s.Owner)
			delete(caller.Permissions, perm.PaymentCollect)
			r := ch.Call(t, caller, tc.op, tc.in)
			if r.Status != 403 || r.Detail() != ops.ErrForbidden.Error() {
				t.Fatal(tc.op, r)
			}
			if ch.Name == "ai" && (tc.op == "cancel_collection" || tc.op == "refund_collection") {
				r = ch.Call(t, owner, tc.op, tc.in)
				optest.RequireDraftCreated(t, r, snapshot(t, s) == before)
			}
		}
		caller := s.MustCaller(s.Owner)
		caller.StoreID = uuid.New()
		r = ch.Call(t, caller, "list_collections", collections.ListCollectionsInput{OrderID: row.OrderID})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 0 {
			t.Fatal(r)
		}
		r = ch.Call(t, caller, "get_collection", collections.GetCollectionInput{ID: row.ID})
		if r.Status != 404 || r.Detail() != ops.ErrNotFound.Error() {
			t.Fatal(r)
		}
	}
	for _, op := range []string{"cancel_collection", "refund_collection"} {
		caller := s.MustCaller(s.Owner)
		caller.StoreID = uuid.New()
		r := s.HTTP(t, &caller, op, collections.ChangeCollectionInput{ID: row.ID, Reason: "退"})
		if r.Status != 404 || r.Detail() != ops.ErrNotFound.Error() {
			t.Fatal(r)
		}
	}
	if snapshot(t, s) != before {
		t.Fatal("rejected requests changed data")
	}
}

func TestChangeWaitsForLock(t *testing.T) {
	for _, op := range []string{"cancel_collection", "refund_collection"} {
		t.Run(op, func(t *testing.T) {
			s := optest.OpenShop(t)
			row := initiate(t, s, "cash", order(t, s))
			held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, held, "SELECT id FROM collections WHERE id=$1 FOR NO KEY UPDATE", row.ID)
			dbtest.Exec(t, held, "UPDATE collections SET status='failed' WHERE id=$1", row.ID)
			done := make(chan optest.Result, 1)
			go func() {
				done <- s.HTTPWithToken(t, s.Owner, op, collections.ChangeCollectionInput{ID: row.ID, Reason: "退"})
			}()
			optest.AssertWaiting(t, s.Pool, done, 1)
			dbtest.Commit(t, held)
			r := optest.WaitResult(t, done, 409)
			target := "cancelled"
			if op == "refund_collection" {
				target = "refunded"
			}
			if r.Detail() != "收款状态不能转换：failed → "+target {
				t.Fatal(r)
			}
		})
	}
}

func TestDatabaseGuards(t *testing.T) {
	s := optest.OpenShop(t)
	o := order(t, s)
	row := initiate(t, s, "cash", o)
	for _, sql := range []string{"UPDATE collections SET amount=2", "UPDATE collections SET order_id=gen_random_uuid()", "DELETE FROM collections", "UPDATE collection_events SET reason='tamper'", "DELETE FROM collection_events"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatal(sql, err)
		}
		dbtest.Rollback(t, tx)
	}
	for _, tc := range []struct {
		name   string
		order  *uuid.UUID
		value  string
		status string
		code   string
	}{{"missing", nil, "1", "pending", "23502"}, {"unknown", new(uuid.New()), "1", "pending", "23503"}, {"zero", &o, "0", "pending", "23514"}, {"negative", &o, "-1", "pending", "23514"}, {"state", &o, "1", "bad", "23514"}} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), "INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',$4,$5,$6,now(),now())", s.Res.TenantID, s.Res.StoreID, tc.order, tc.value, tc.status, s.Res.OwnerID)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != tc.code {
				t.Fatal(err)
			}
			dbtest.Rollback(t, tx)
		})
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	env := ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner), Now: s.Now()}
	sum, err := collections.Query(context.Background(), env, row.ID)
	if err != nil || sum.Status != "succeeded" {
		t.Fatal(sum, err)
	}
	dbtest.Rollback(t, tx)
}

// fakeChannel 让新增通道走相同的发起、查询、撤销、退款及设置路径。
var fakeCancels atomic.Int64
var fakeRefunds atomic.Int64

type fakeChannel struct{}

func (fakeChannel) Initiate(_ context.Context, _ ops.Env, row dbgen.Collection) (collections.Result, error) {
	return collections.Result{Status: "processing", ExternalRef: row.ExternalRef}, nil
}
func (fakeChannel) Query(_ context.Context, _ ops.Env, row dbgen.Collection) (collections.Result, error) {
	return collections.Result{Status: row.Status}, nil
}
func (fakeChannel) Cancel(context.Context, ops.Env, dbgen.Collection) error {
	fakeCancels.Add(1)
	return nil
}
func (fakeChannel) Refund(context.Context, ops.Env, dbgen.Collection) error {
	fakeRefunds.Add(1)
	return nil
}
func (fakeChannel) Reconcile(_ context.Context, _ ops.Env, _ time.Time) (collections.Summary, error) {
	return collections.Summary{Channel: "test_channel"}, nil
}

type succeedingChannel struct{}

func (succeedingChannel) Initiate(_ context.Context, _ ops.Env, row dbgen.Collection) (collections.Result, error) {
	return collections.Result{Status: "succeeded", ExternalRef: row.ExternalRef}, nil
}
func (succeedingChannel) Query(_ context.Context, _ ops.Env, row dbgen.Collection) (collections.Result, error) {
	return collections.Result{Status: row.Status}, nil
}
func (succeedingChannel) Cancel(context.Context, ops.Env, dbgen.Collection) error {
	fakeCancels.Add(1)
	return nil
}
func (succeedingChannel) Refund(context.Context, ops.Env, dbgen.Collection) error {
	fakeRefunds.Add(1)
	return nil
}
func (succeedingChannel) Reconcile(_ context.Context, _ ops.Env, _ time.Time) (collections.Summary, error) {
	return collections.Summary{Channel: "test_success_channel"}, nil
}

func init() {
	collections.RegisterChannel(collections.Registration{Code: "test_channel", Name: "测试通道", Channel: fakeChannel{}})
	collections.RegisterChannel(collections.Registration{Code: "test_success_channel", Name: "测试成功通道", Channel: succeedingChannel{}})
}
func TestRegisteredChannel(t *testing.T) {
	s := optest.OpenShop(t)
	row := initiate(t, s, "test_channel", order(t, s))
	if row.Status != "processing" {
		t.Fatal(row)
	}
	def, ok := settings.Lookup("collection_channel_test_channel")
	if !ok || def.Kind != settings.KindChoice || def.Default != "on" {
		t.Fatal(def)
	}
	call(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID})
}

func TestSuccessfulChannelEffects(t *testing.T) {
	s := optest.OpenShop(t)
	cancel := initiate(t, s, "test_success_channel", order(t, s))
	beforeCancel := fakeCancels.Load()
	call(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: cancel.ID})
	if fakeCancels.Load() != beforeCancel+1 {
		t.Fatal("successful cancel did not call the channel")
	}
	refund := initiate(t, s, "test_success_channel", order(t, s))
	beforeRefund := fakeRefunds.Load()
	call(t, s, "refund_collection", collections.ChangeCollectionInput{ID: refund.ID, Reason: "退款"})
	if fakeRefunds.Load() != beforeRefund+1 {
		t.Fatal("successful refund did not call the channel")
	}
}

func TestCollectionRollback(t *testing.T) {
	s := optest.OpenShop(t)
	o := order(t, s)
	row := initiate(t, s, "cash", o)
	// 让审计插入失败，状态和事件必须随同一事务回滚。
	if _, err := s.Pool.Exec(context.Background(), "ALTER TABLE audit_log ADD CONSTRAINT collection_test_failure CHECK (action <> 'cancel_collection')"); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, s)
	r := s.HTTPWithToken(t, s.Owner, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID})
	if r.Status != 500 || r.Detail() == "" || snapshot(t, s) != before {
		t.Fatal(r, "partial write")
	}
}

func TestCollectionForeignKeys(t *testing.T) {
	s := optest.OpenShop(t)
	o := order(t, s)
	row := initiate(t, s, "cash", o)
	otherTenant, otherStore := uuid.New(), uuid.New()
	// 只用于外键与 RLS 的另租户资料，真实目标必须存在才会暴露漏掉 tenant_id 的外键。
	if _, err := s.Pool.Exec(context.Background(), "INSERT INTO tenants(id,name) VALUES($1,'另租户')", otherTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(context.Background(), "INSERT INTO stores(id,tenant_id,name) VALUES($1,$2,'另店')", otherStore, otherTenant); err != nil {
		t.Fatal(err)
	}
	var otherStaff, otherCustomer, otherSnapshot, otherOrder, otherCheckout, otherPayment, otherCollection uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO staff(tenant_id,store_id,name,number) VALUES($1,$2,'别员工','001') RETURNING id`, otherTenant, otherStore).Scan(&otherStaff); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO customers(tenant_id,store_id,name,created_by) VALUES($1,$2,'别顾客',$3) RETURNING id`, otherTenant, otherStore, otherStaff).Scan(&otherCustomer); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO settings_snapshots(tenant_id,store_id,rules,digest) VALUES($1,$2,'{}','other') RETURNING id`, otherTenant, otherStore).Scan(&otherSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO orders(tenant_id,store_id,customer_id,gender,designated,settings_snapshot_id,business_date,opened_by) VALUES($1,$2,$3,'female',false,$4,current_date,$5) RETURNING id`, otherTenant, otherStore, otherCustomer, otherSnapshot, otherStaff).Scan(&otherOrder); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO checkouts(tenant_id,store_id,business_date,checked_out_by) VALUES($1,$2,current_date,$3) RETURNING id`, otherTenant, otherStore, otherStaff).Scan(&otherCheckout); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount) VALUES($1,$2,$3,$4,'cash',1) RETURNING id`, otherTenant, otherStore, otherCheckout, otherOrder).Scan(&otherPayment); err != nil {
		t.Fatal(err)
	}
	var otherPayment2 uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO order_payments(tenant_id,store_id,checkout_id,order_id,method,amount) VALUES($1,$2,$3,$4,'cash',1) RETURNING id`, otherTenant, otherStore, otherCheckout, otherOrder).Scan(&otherPayment2); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO collections(tenant_id,store_id,order_id,order_payment_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,'cash',1,'succeeded',$5,now(),now()) RETURNING id`, otherTenant, otherStore, otherOrder, otherPayment, otherStaff).Scan(&otherCollection); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"order", `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',1,'pending',$4,now(),now())`, []any{s.Res.TenantID, s.Res.StoreID, otherOrder, s.Res.OwnerID}},
		{"store", `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',1,'pending',$4,now(),now())`, []any{s.Res.TenantID, otherStore, o, s.Res.OwnerID}},
		{"creator", `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',1,'pending',$4,now(),now())`, []any{s.Res.TenantID, s.Res.StoreID, o, otherStaff}},
		{"payment", `INSERT INTO collections(tenant_id,store_id,order_id,order_payment_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,'cash',1,'pending',$5,now(),now())`, []any{s.Res.TenantID, s.Res.StoreID, o, otherPayment2, s.Res.OwnerID}},
		{"event collection", `INSERT INTO collection_events(tenant_id,store_id,collection_id,from_status,to_status,actor_id,at,reason) VALUES($1,$2,$3,'pending','succeeded',$4,now(),'x')`, []any{s.Res.TenantID, s.Res.StoreID, otherCollection, s.Res.OwnerID}},
		{"event actor", `INSERT INTO collection_events(tenant_id,store_id,collection_id,from_status,to_status,actor_id,at,reason) VALUES($1,$2,$3,'pending','succeeded',$4,now(),'x')`, []any{s.Res.TenantID, s.Res.StoreID, row.ID, otherStaff}},
		{"event store", `INSERT INTO collection_events(tenant_id,store_id,collection_id,from_status,to_status,actor_id,at,reason) VALUES($1,$2,$3,'pending','succeeded',$4,now(),'x')`, []any{s.Res.TenantID, otherStore, row.ID, s.Res.OwnerID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "23503" {
				t.Fatal(err)
			}
			dbtest.Rollback(t, tx)
		})
	}
	// 本租户的行不能由另一个租户读取；查询显式使用原门店，避免门店筛选掩盖 RLS。
	tx := dbtest.AppTx(t, s.Pool, otherTenant)
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM collections WHERE id=$1", row.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM collection_events WHERE collection_id=$1", row.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
	for _, table := range []string{"collections", "collection_events"} {
		tx := dbtest.AppTx(t, s.Pool, otherTenant)
		query := `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',1,'pending',$4,now(),now())`
		args := []any{s.Res.TenantID, s.Res.StoreID, o, s.Res.OwnerID}
		if table == "collection_events" {
			query = `INSERT INTO collection_events(tenant_id,store_id,collection_id,from_status,to_status,actor_id,at,reason) VALUES($1,$2,$3,'pending','succeeded',$4,now(),'x')`
			args[2] = row.ID
		}
		_, err := tx.Exec(context.Background(), query, args...)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
	}
}

func TestListCollectionOrder(t *testing.T) {
	s := optest.OpenShop(t)
	o := order(t, s)
	row := initiate(t, s, "cash", o)
	low := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO collections(id,tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,'cash',1,'pending',$5,now(),now())`, low, s.Res.TenantID, s.Res.StoreID, o, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	// 关掉索引扫描，避免 (store_id,order_id,id) 索引掩盖漏写的 ORDER BY。
	config := s.Pool.Config().Copy()
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = map[string]string{}
	}
	for _, name := range []string{"enable_indexscan", "enable_indexonlyscan", "enable_bitmapscan"} {
		config.ConnConfig.RuntimeParams[name] = "off"
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	app := optest.New(t, pool)
	for _, ch := range app.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_collections", collections.ListCollectionsInput{OrderID: o})
		if r.Status != 200 {
			t.Fatal(r)
		}
		rows := r.Body["items"].([]any)
		if len(rows) != 2 || rows[0].(map[string]any)["id"] != low.String() || rows[1].(map[string]any)["id"] != row.ID.String() {
			t.Fatal(rows)
		}
	}
}

func TestRegistrationValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    collections.Registration
		want string
	}{
		{"code", collections.Registration{Channel: fakeChannel{}}, "collections: invalid channel"},
		{"nil", collections.Registration{Code: "nil_channel"}, "collections: invalid channel"},
		{"duplicate", collections.Registration{Code: "cash", Channel: fakeChannel{}}, "settings: duplicate key collection_channel_cash"},
		{"method", collections.Registration{Code: "duplicate_method", PaymentMethod: "cash", Channel: fakeChannel{}}, "collections: duplicate payment method"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.want {
					t.Fatalf("got %v want %s", got, tc.want)
				}
			}()
			collections.RegisterChannel(tc.r)
		})
	}
	t.Run("setting", func(t *testing.T) {
		defer func() {
			if got := recover(); got != "settings: duplicate key collection_channel_cash" {
				t.Fatal(got)
			}
		}()
		settings.Declare(settings.Def{Key: "collection_channel_cash"})
	})
}

func TestRequiredColumns(t *testing.T) {
	s := optest.OpenShop(t)
	row := initiate(t, s, "cash", order(t, s))
	now := s.Now()
	for _, tc := range []struct {
		table, columns, values string
		names                  []string
		args                   []any
	}{
		{"collections", "tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at", "$1,$2,$3,$4,$5,$6,$7,$8,$9", []string{"tenant_id", "store_id", "order_id", "channel", "amount", "status", "created_by", "created_at", "updated_at"}, []any{s.Res.TenantID, s.Res.StoreID, row.OrderID, "cash", "1", "pending", s.Res.OwnerID, now, now}},
		{"collection_events", "tenant_id,store_id,collection_id,from_status,to_status,actor_id,at,reason", "$1,$2,$3,$4,$5,$6,$7,$8", []string{"tenant_id", "store_id", "collection_id", "from_status", "to_status", "actor_id", "at", "reason"}, []any{s.Res.TenantID, s.Res.StoreID, row.ID, "pending", "succeeded", s.Res.OwnerID, now, "发起"}},
	} {
		for i, name := range tc.names {
			t.Run(tc.table+"/"+name, func(t *testing.T) {
				args := append([]any{}, tc.args...)
				args[i] = nil
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				if name == "tenant_id" {
					var notNull bool
					if err := tx.QueryRow(context.Background(), "SELECT attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname='tenant_id'", tc.table).Scan(&notNull); err != nil || !notNull {
						t.Fatal(notNull, err)
					}
				}
				_, err := tx.Exec(context.Background(), "INSERT INTO "+tc.table+"("+tc.columns+") VALUES("+tc.values+")", args...)
				var pe *pgconn.PgError
				want := "23502"
				if name == "tenant_id" {
					want = "42501"
				}
				if !errors.As(err, &pe) || pe.Code != want || (want == "23502" && pe.ColumnName != name) {
					t.Fatal(err)
				}
				dbtest.Rollback(t, tx)
			})
		}
	}
}

func TestNoForeignChannelEffect(t *testing.T) {
	s := optest.OpenShop(t)
	row := initiate(t, s, "test_channel", order(t, s))
	caller := s.MustCaller(s.Owner)
	caller.StoreID = uuid.New()
	before := fakeCancels.Load()
	r := s.HTTP(t, &caller, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID})
	if r.Status != 404 || r.Detail() != ops.ErrNotFound.Error() || fakeCancels.Load() != before {
		t.Fatal(r, "foreign channel action")
	}
	call(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID})
	before = fakeCancels.Load()
	refuse(t, s, "cancel_collection", collections.ChangeCollectionInput{ID: row.ID}, "收款状态不能转换：cancelled → cancelled")
	if fakeCancels.Load() != before {
		t.Fatal("terminal channel action")
	}
}

func TestReconcileScope(t *testing.T) {
	s := optest.OpenShop(t)
	now := time.Date(2027, 3, 5, 1, 30, 0, 0, settings.Location)
	s.SetNow(now)
	o := order(t, s)
	initiate(t, s, "cash", o)
	refunded := initiate(t, s, "cash", o)
	call(t, s, "refund_collection", collections.ChangeCollectionInput{ID: refunded.ID, Reason: "退"})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "分店"})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',7,'succeeded',$4,$5,$5)`, s.Res.TenantID, store.ID, o, s.Res.OwnerID, now)
	env := ops.Env{Q: q, Caller: s.MustCaller(s.Owner), Now: now}
	reg, err := collections.Lookup("cash")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := reg.Channel.Reconcile(context.Background(), env, settings.Date(now))
	if err != nil || !reflect.DeepEqual(sum.Amounts, map[string]string{"succeeded": "10.00", "refunded": "10.00"}) || !reflect.DeepEqual(sum.Counts, map[string]int{"succeeded": 1, "refunded": 1}) {
		t.Fatal(sum, err)
	}
	dbtest.Rollback(t, tx)
}

func TestReconcileBusinessDateAcrossMidnight(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2029, 12, 31, 12, 0, 0, 0, settings.Location))
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": "2030-01-01"}, 200)
	now := time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location)
	s.SetNow(now)
	row := initiate(t, s, "cash", order(t, s))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	env := ops.Env{Q: dbgen.New(tx), Caller: s.MustCaller(s.Owner), Now: now}
	reg, err := collections.Lookup("cash")
	if err != nil {
		t.Fatal(err)
	}
	first, err := reg.Channel.Reconcile(context.Background(), env, settings.Date(now).AddDate(0, 0, -1))
	if err != nil || first.Amounts["succeeded"] != row.Amount.String() || first.Counts["succeeded"] != 1 {
		t.Fatal(first, err)
	}
	second, err := reg.Channel.Reconcile(context.Background(), env, settings.Date(now))
	if err != nil || len(second.Amounts) != 0 || len(second.Counts) != 0 {
		t.Fatal(second, err)
	}
	dbtest.Rollback(t, tx)
}

func TestCollectionMigrationDownAuditCheck(t *testing.T) {
	s := optest.OpenShop(t)
	dbtest.MigrateDownTo(t, s.Pool, 37)
	for _, tc := range []struct {
		kind string
		code string
	}{{"collection", "23514"}, {"payroll_month", "00000"}} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, tc.kind, uuid.New())
		dbtest.Rollback(t, tx)
		if tc.code == "00000" {
			if err != nil {
				t.Fatal(tc.kind, err)
			}
			continue
		}
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != tc.code || pe.ConstraintName != "audit_log_target_kind_check" {
			t.Fatal(tc.kind, err)
		}
	}
}
