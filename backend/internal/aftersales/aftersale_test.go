package aftersales_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	customer uuid.UUID
	item     uuid.UUID
	position uuid.UUID
	level    uuid.UUID
	employee uuid.UUID
}

func open(t *testing.T) *fixture {
	t.Helper()
	s := optest.OpenShop(t)
	f := &fixture{Shop: s, t: t}
	f.customer = id(s.Call(t, s.Owner, "create_customer", map[string]any{"name": "顾客", "phone": "13800000001"}, 200).Body["customer"].(map[string]any)["id"])
	f.position = id(f.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	f.level = id(f.call("create_level", map[string]any{"name": "首席"}, 200)["id"])
	f.employee, _ = s.Hire("员工", "002", "staff")
	f.call("update_staff", map[string]any{"staff_id": f.employee, "name": "员工", "number": "002", "level_id": f.level, "position_id": nil}, 200)
	f.item = id(f.call("create_item", map[string]any{
		"name": "售后项目", "price": "100.00", "discountable": true,
		"labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": f.position, "required": true}},
		"level_prices": []any{},
	}, 200)["id"])
	return f
}

func at(day string, hour int) time.Time {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, settings.Location)
}

func (f *fixture) now(day string, hour int) { f.SetNow(at(day, hour)) }
func (f *fixture) today() string            { return settings.Date(f.Now()).Format(settings.DateLayout) }

func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func (f *fixture) call(op string, in any, want int) map[string]any {
	f.t.Helper()
	return f.Call(f.t, f.Owner, op, in, want).Body
}

func (f *fixture) setting(key string, value any) {
	f.t.Helper()
	f.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": f.today()}, 200)
}

func (f *fixture) order(lines ...int) map[string]any {
	f.t.Helper()
	items := []any{}
	for _, slot := range lines {
		staff := []any{}
		if slot > 0 {
			staff = append(staff, map[string]any{"slot": slot, "staff_id": f.employee})
		}
		items = append(items, map[string]any{"item_id": f.item, "staff": staff})
	}
	return f.call("open_order", map[string]any{"customer_id": f.customer, "gender": "female", "designated": true, "items": items}, 200)
}

func payment(method, amount string) map[string]any {
	return map[string]any{"method": method, "amount": amount}
}

func cardPayment(method, amount string, cardID uuid.UUID, itemID *uuid.UUID) map[string]any {
	p := payment(method, amount)
	p["card_id"] = cardID
	if itemID != nil {
		p["order_item_id"] = *itemID
	}
	return p
}

func (f *fixture) checkout(o map[string]any, payments ...map[string]any) map[string]any {
	f.t.Helper()
	if payments == nil {
		payments = []map[string]any{}
	}
	body := f.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": o["id"], "payments": payments}}}, 200)
	return body["items"].([]any)[0].(map[string]any)
}

func (f *fixture) paidOrder(payments ...map[string]any) map[string]any {
	f.t.Helper()
	return f.checkout(f.order(1), payments...)
}

func (f *fixture) cardType(name string) uuid.UUID {
	f.t.Helper()
	return id(f.call("create_card_type", map[string]any{"name": name, "kind": "stored_value", "validity_kind": "none"}, 200)["id"])
}

func (f *fixture) card(ct uuid.UUID, principal, bonus string, payments ...map[string]any) map[string]any {
	f.t.Helper()
	if payments == nil {
		payments = []map[string]any{}
	}
	list := make([]any, len(payments))
	for i, p := range payments {
		list[i] = p
	}
	return f.call("open_card", map[string]any{"customer_id": f.customer, "card_type_id": ct, "principal": principal, "bonus": bonus, "sales_staff_ids": []any{f.Res.OwnerID}, "payments": list}, 200)
}

func (f *fixture) create(orderID uuid.UUID, typ, reason string, lines ...map[string]any) map[string]any {
	f.t.Helper()
	in := map[string]any{"order_id": orderID, "type": typ, "reason": reason}
	if lines != nil {
		refundLines := make([]any, len(lines))
		for i, line := range lines {
			refundLines[i] = line
		}
		in["refund_lines"] = refundLines
	}
	return f.call("create_aftersale", in, 200)
}

func (f *fixture) approve(aftersaleID uuid.UUID, extra map[string]any) map[string]any {
	f.t.Helper()
	in := map[string]any{"id": aftersaleID}
	for key, value := range extra {
		in[key] = value
	}
	return f.call("approve_aftersale", in, 200)
}

func (f *fixture) reject(aftersaleID uuid.UUID, note *string) map[string]any {
	f.t.Helper()
	in := map[string]any{"id": aftersaleID}
	if note != nil {
		in["note"] = *note
	}
	return f.call("reject_aftersale", in, 200)
}

func (f *fixture) snapshot() string {
	f.t.Helper()
	return dbtest.Snapshot(f.t, f.Pool, f.Res.TenantID, "aftersales", "aftersale_events", "aftersale_refunds", "orders", "order_payments", "cards", "card_ledger", "performance_entries", "audit_log")
}

func (f *fixture) refused(caller ops.Caller, op string, in any, status int, detail string) optest.Result {
	f.t.Helper()
	before := f.snapshot()
	r := f.HTTP(f.t, &caller, op, in)
	if r.Status != status || (detail != "" && r.Detail() != detail) || before != f.snapshot() {
		f.t.Fatalf("%s: got %d %q, want %d %q, changed=%v", op, r.Status, r.Detail(), status, detail, before != f.snapshot())
	}
	return r
}

func TestAftersaleCreateValidation(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	openOrder := f.order(1)
	f.now("2026-10-02", 12)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": openOrder["id"], "type": "repair", "reason": "原因"}, 409, "只能为已结账的单创建售后单")
	cash := f.checkout(openOrder, payment("cash", "100"))
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "repair", "reason": "  "}, 409, "请填写售后原因")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "repair", "reason": "原因", "refund_lines": []any{payment("cash", "1")}}, 409, "修补、重做不能填写退款")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "partial_refund", "reason": "原因"}, 409, "部分退款至少填写一笔退款")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{payment("cash", "0")}}, 422, "invalid arguments: 退款金额必须大于零")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{payment("cash", "101")}}, 409, "退款合计不能超过可退金额 100.00")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "full_refund", "reason": "原因", "refund_lines": []any{payment("cash", "99")}}, 409, "全额退款合计必须等于可退金额 100.00")

	keep, dropped := f.order(1), f.order(1)
	f.call("merge_orders", map[string]any{"order_id": keep["id"], "merged_order_ids": []any{dropped["id"]}}, 200)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": dropped["id"], "type": "repair", "reason": "原因"}, 409, "单已合并，请在合并后的单上操作")

	voided := f.paidOrder(payment("cash", "100"))
	f.call("void_order", map[string]any{"order_id": voided["id"], "reason": "录入错误"}, 200)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": voided["id"], "type": "repair", "reason": "原因"}, 409, "单已撤")

	f.now("2026-11-02", 12)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": cash["id"], "type": "repair", "reason": "原因"}, 409, "已超过售后期限（30天）")
	f.now("2026-10-02", 12)
	caller := f.MustCaller(f.Owner)
	delete(caller.Permissions, "aftersales.manage")
	f.refused(caller, "create_aftersale", map[string]any{"order_id": cash["id"], "type": "repair", "reason": "原因"}, 403, "permission denied")
	other := f.MustCaller(f.Owner)
	other.StoreID = uuid.New()
	f.refused(other, "create_aftersale", map[string]any{"order_id": cash["id"], "type": "repair", "reason": "原因"}, 404, "not found")
}

func TestAftersaleWindowReadsCurrentBusinessDate(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-31", 12)
	f.setting("aftersale_window_days", 60)
	f.now("2026-11-02", 12)
	if got := f.create(id(order["id"]), "repair", "当前营业日的期限"); got["status"] != "pending" {
		t.Fatal(got)
	}
}

func TestAftersaleCardPrincipalValidation(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	ct := f.cardType("储值卡")
	card := f.card(ct, "100.00", "0.00", payment("cash", "100"))
	order := f.order(1)
	f.checkout(order, payment("cash", "50"), cardPayment("card", "50", id(card["id"]), nil))
	f.now("2026-10-02", 12)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{map[string]any{"method": "card_principal", "amount": "10"}}}, 422, "invalid arguments: 卡本金退款必须指定卡")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{map[string]any{"method": "card_principal", "amount": "10", "card_id": uuid.New()}}}, 409, "卡本金退款只能退回原单支付过本金的卡")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{map[string]any{"method": "card_principal", "amount": "101", "card_id": card["id"]}}}, 409, "退款合计不能超过可退金额 100.00")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{map[string]any{"method": "card_principal", "amount": "50", "card_id": card["id"]}, map[string]any{"method": "card_principal", "amount": "1", "card_id": card["id"]}}}, 409, "卡本金退款超过这张卡原单支付的本金")
	quote := f.call("preview_card_refund", map[string]any{"card_id": card["id"]}, 200)
	f.call("refund_card", map[string]any{"card_id": card["id"], "method": "cash", "amount": quote["amount"]}, 200)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{map[string]any{"method": "card_principal", "amount": "10", "card_id": card["id"]}}}, 409, "卡已退，不能退回本金")

	// 创建后卡才退：审批时在锁内重新检查。
	f2 := open(t)
	f2.now("2026-10-01", 12)
	ct2 := f2.cardType("待退卡")
	card2 := f2.card(ct2, "100.00", "0.00", payment("cash", "100"))
	order2 := f2.order(1)
	f2.checkout(order2, cardPayment("card", "100", id(card2["id"]), nil))
	f2.now("2026-10-02", 12)
	pending := f2.create(id(order2["id"]), "partial_refund", "稍后退", map[string]any{"method": "card_principal", "amount": "10", "card_id": card2["id"]})
	quote2 := f2.call("preview_card_refund", map[string]any{"card_id": card2["id"]}, 200)
	f2.call("refund_card", map[string]any{"card_id": card2["id"], "method": "cash", "amount": quote2["amount"]}, 200)
	f2.refused(f2.MustCaller(f2.Owner), "approve_aftersale", map[string]any{"id": pending["id"]}, 409, "卡已退，不能退回本金")
}

func TestAftersaleRefundableMoney(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	ct := f.cardType("本金赠送")
	card := f.card(ct, "50.00", "50.00", payment("cash", "50"))
	order := f.order(1)
	f.checkout(order, payment("cash", "40"), cardPayment("card", "60", id(card["id"]), nil))
	f.now("2026-10-02", 12)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "原因", "refund_lines": []any{payment("cash", "100")}}, 409, "全额退款合计必须等于可退金额 90.00")
	first := f.create(id(order["id"]), "partial_refund", "退一部分", payment("cash", "30"))
	f.approve(id(first["id"]), nil)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "原因", "refund_lines": []any{payment("cash", "61")}}, 409, "退款合计不能超过可退金额 60.00")
	second := f.create(id(order["id"]), "full_refund", "退剩余", payment("unionpay", "60"))
	out := f.approve(id(second["id"]), nil)
	if len(out["refunds"].([]any)) != 1 || out["refunds"].([]any)[0].(map[string]any)["business_date"] != "2026-10-02" {
		t.Fatal(out)
	}
}

func TestAftersaleDebtNotRefundable(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "50"), payment("debt", "50"))
	f.now("2026-10-02", 12)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "原因", "refund_lines": []any{payment("cash", "100")}}, 409, "全额退款合计必须等于可退金额 50.00")
	first := f.create(id(order["id"]), "full_refund", "退现金", payment("cash", "50"))
	f.approve(id(first["id"]), nil)
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "再退"}, 409, "没有可退金额")
}

func TestAftersaleApproveRejectAndEvents(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	created := f.create(id(order["id"]), "partial_refund", "退一半", payment("wechat", "50"))
	if created["status"] != "pending" || len(created["refunds"].([]any)) != 0 || len(created["events"].([]any)) != 1 {
		t.Fatal(created)
	}
	before := f.snapshot()
	if got := f.call("get_aftersale", map[string]any{"id": created["id"]}, 200); got["status"] != "pending" {
		t.Fatal(got)
	}
	if before != f.snapshot() {
		t.Fatal("reading aftersale changed data")
	}
	approved := f.approve(id(created["id"]), nil)
	if approved["status"] != "approved" || approved["approved_by"].(map[string]any)["id"] != f.Res.OwnerID.String() || len(approved["refunds"].([]any)) != 1 || len(approved["events"].([]any)) != 2 || approved["deduct_performance"] != false || approved["deduct_commission"] != false {
		t.Fatal(approved)
	}
	f.refused(f.MustCaller(f.Owner), "approve_aftersale", map[string]any{"id": created["id"]}, 409, "售后单已审批")
	f.refused(f.MustCaller(f.Owner), "reject_aftersale", map[string]any{"id": created["id"], "note": "晚了一步"}, 409, "售后单已审批")

	second := f.create(id(order["id"]), "redo", "重做")
	note := "不需要"
	rejected := f.reject(id(second["id"]), &note)
	if rejected["status"] != "rejected" || rejected["reject_note"] != note || rejected["rejected_by"].(map[string]any)["id"] != f.Res.OwnerID.String() {
		t.Fatal(rejected)
	}
	f.refused(f.MustCaller(f.Owner), "reject_aftersale", map[string]any{"id": second["id"]}, 409, "售后单已拒绝")
	f.refused(f.MustCaller(f.Owner), "approve_aftersale", map[string]any{"id": second["id"]}, 409, "售后单已拒绝")

	rejectOnly := f.create(id(order["id"]), "repair", "修补")
	f.reject(id(rejectOnly["id"]), nil)
	// 拒绝只改状态和事件，不写退款、卡流水或业绩。
	if f.call("get_aftersale", map[string]any{"id": rejectOnly["id"]}, 200)["status"] != "rejected" {
		t.Fatal("not rejected")
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var refunds, ledger, performance int
	if err := tx.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM aftersale_refunds WHERE aftersale_id=$1),(SELECT count(*) FROM card_ledger WHERE aftersale_id=$1),(SELECT count(*) FROM performance_entries WHERE aftersale_id=$1)", rejectOnly["id"]).Scan(&refunds, &ledger, &performance); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if refunds != 0 || ledger != 0 || performance != 0 {
		t.Fatal(refunds, ledger, performance)
	}
}

func TestAftersaleApprovePermission(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	refund := f.create(id(order["id"]), "repair", "待审批")
	denied := f.MustCaller(f.Owner)
	delete(denied.Permissions, "aftersales.manage")
	f.refused(denied, "approve_aftersale", map[string]any{"id": refund["id"]}, 403, "permission denied")
	f.refused(denied, "reject_aftersale", map[string]any{"id": refund["id"]}, 403, "permission denied")
}

func TestAftersaleApprovalMode(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	f.setting("aftersale_approval_mode", "other")
	mine := f.create(id(order["id"]), "repair", "本人开单")
	f.refused(f.MustCaller(f.Owner), "approve_aftersale", map[string]any{"id": mine["id"]}, 409, "售后单须由另一名员工审批")
	f.refused(f.MustCaller(f.Owner), "reject_aftersale", map[string]any{"id": mine["id"]}, 409, "售后单须由另一名员工审批")
	other, token := f.Hire("店长", "003", "manager", f.Role("店长"))
	_ = other
	caller := f.MustCaller(token)
	r := f.HTTP(t, &caller, "approve_aftersale", map[string]any{"id": mine["id"]})
	if r.Status != 200 || r.Body["status"] != "approved" {
		t.Fatal(r)
	}
	f.setting("aftersale_approval_mode", "self")
	again := f.create(id(order["id"]), "repair", "本人开单")
	if got := f.approve(id(again["id"]), nil); got["status"] != "approved" {
		t.Fatal(got)
	}
}

func TestAftersaleVoidRejection(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	pending := f.create(id(order["id"]), "partial_refund", "待审批", payment("cash", "10"))
	f.call("void_order", map[string]any{"order_id": order["id"], "reason": "录入错误"}, 200)
	f.refused(f.MustCaller(f.Owner), "approve_aftersale", map[string]any{"id": pending["id"]}, 409, "原单已撤，售后单不能生效")

	other := f.paidOrder(payment("cash", "100"))
	refund := f.create(id(other["id"]), "partial_refund", "已审批", payment("cash", "10"))
	f.approve(id(refund["id"]), nil)
	f.refused(f.MustCaller(f.Owner), "void_order", map[string]any{"order_id": other["id"], "reason": "录入错误"}, 409, "有已生效的退款售后单，不能撤单")
}

func TestAftersaleDatabaseProtections(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	created := f.create(id(order["id"]), "partial_refund", "退款", payment("cash", "10"))
	f.approve(id(created["id"]), nil)

	for _, table := range []string{"aftersales", "aftersale_events", "aftersale_refunds"} {
		tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
		if _, err := tx.Exec(context.Background(), "DELETE FROM "+table); err == nil {
			t.Fatal(table, "delete allowed")
		} else if pe, ok := err.(*pgconn.PgError); !ok || pe.Code != "42501" {
			t.Fatal(table, err)
		}
		dbtest.Rollback(t, tx)
	}

	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := tx.Exec(context.Background(), "UPDATE aftersales SET reason='改变原因'")
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)

	other := otherTenant(t, f.Pool)
	tx = dbtest.AppTx(t, f.Pool, other.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM aftersales WHERE store_id=$1", f.Res.StoreID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM aftersale_refunds WHERE store_id=$1", f.Res.StoreID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	dbtest.Rollback(t, tx)
}

func otherTenant(t *testing.T, pool *pgxpool.Pool) staff.BootstrapResult {
	t.Helper()
	res, err := staff.Bootstrap(context.Background(), pool, staff.BootstrapInput{TenantName: "另租户", StoreName: "另店", OwnerName: "别老板", OwnerNumber: "001", OwnerAccount: "other-boss", OwnerPassword: "other-password"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *fixture) simpleItem(name, price string) uuid.UUID {
	f.t.Helper()
	return id(f.call("create_item", map[string]any{"name": name, "price": price, "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200)["id"])
}

func (f *fixture) nonMoneyOrder(items ...uuid.UUID) map[string]any {
	f.t.Helper()
	lines := []any{}
	for _, item := range items {
		lines = append(lines, map[string]any{"item_id": item, "staff": []any{}})
	}
	return f.call("open_order", map[string]any{"customer_id": f.customer, "gender": "female", "designated": false, "items": lines}, 200)
}

func TestAftersaleRefundableExcludesNonMoneyMethods(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		f := open(t)
		f.now("2026-10-01", 12)
		cashItem := f.simpleItem("现金项目", "40.00")
		countItem := f.simpleItem("扣次项目", "60.00")
		order := f.nonMoneyOrder(cashItem, countItem)
		line := order["items"].([]any)[1].(map[string]any)
		ct := f.call("create_card_type", map[string]any{"name": "扣次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": countItem, "paid_count": 1, "gift_count": 0, "unit_price": "60.00"}}}, 200)
		card := f.call("open_card", map[string]any{"customer_id": f.customer, "card_type_id": ct["id"], "principal": "0.00", "bonus": "0.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "60.00")}}, 200)
		f.checkout(order, payment("cash", "40"), cardPayment("count", "60", id(card["id"]), new(id(line["id"]))))
		f.now("2026-10-02", 12)
		f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "原因", "refund_lines": []any{payment("cash", "60")}}, 409, "全额退款合计必须等于可退金额 40.00")
		f.create(id(order["id"]), "full_refund", "只退现金", payment("cash", "40"))
	})

	t.Run("red packet", func(t *testing.T) {
		f := open(t)
		f.now("2026-10-01", 12)
		order := f.order(1)
		packet := f.call("issue_red_packet", map[string]any{"customer_id": f.customer, "kind": "gift", "face_value": "60", "expires_on": "2999-01-01", "payments": []any{}}, 200)
		p := payment("red_packet", "60")
		p["red_packet_id"] = packet["id"]
		f.checkout(order, payment("cash", "40"), p)
		f.now("2026-10-02", 12)
		f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "原因", "refund_lines": []any{payment("cash", "60")}}, 409, "全额退款合计必须等于可退金额 40.00")
		f.create(id(order["id"]), "full_refund", "只退现金", payment("cash", "40"))
	})

	t.Run("groupbuy", func(t *testing.T) {
		f := open(t)
		f.now("2026-10-01", 12)
		fake := fakeprovider.New("douyin", f.Now())
		f.Groupbuy.Register("douyin", fake)
		groupItem := f.simpleItem("团购项目", "60.00")
		cashItem := f.simpleItem("现金项目", "40.00")
		order := f.nonMoneyOrder(groupItem, cashItem)
		deal := f.call("create_groupbuy_deal", map[string]any{"platform": "douyin", "name": "团购券", "purchase_price": "50.00", "item_id": groupItem}, 200)
		f.call("redeem_coupon", map[string]any{"platform": "douyin", "coupon_code": "gb1", "key": "gb1"}, 200)
		redemption := f.call("get_redemption", map[string]any{"key": "gb1"}, 200)
		f.call("link_coupon_order", map[string]any{"redemption_id": redemption["id"], "deal_id": deal["id"], "order_id": order["id"]}, 200)
		paid := f.checkout(order, payment("cash", "40"))
		if len(paid["payments"].([]any)) != 2 {
			t.Fatal(paid)
		}
		f.now("2026-10-02", 12)
		f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "原因", "refund_lines": []any{payment("cash", "60")}}, 409, "全额退款合计必须等于可退金额 40.00")
		f.create(id(order["id"]), "full_refund", "只退现金", payment("cash", "40"))
	})
}
