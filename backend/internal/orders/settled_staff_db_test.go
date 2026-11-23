package orders_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func TestOrderStaffChangeDatabaseConstraints(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	changed := s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	replacement, _ := s.Hire("约束接替", "074", "settled-db")
	s.SetNow(dayAt("2026-10-02"))
	after := s.call("set_paid_item_staff", paidStaffInput(id(changed["id"]), id(firstLine(changed)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: replacement}), 200)
	changeID := id(after["staff_changes"].([]any)[0].(map[string]any)["id"])

	other := s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	voided := s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	voidAfter := s.void(voided)
	voidID := id(voidAfter["void"].(map[string]any)["id"])
	aftersale := s.call("create_aftersale", map[string]any{"order_id": other["id"], "type": "repair", "reason": "约束"}, 200)

	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var source uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT id FROM performance_entries WHERE order_id=$1 AND kind='item' AND reverses_id IS NULL LIMIT 1", id(changed["id"])).Scan(&source); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)

	refused := func(overrides map[string]any, state string) {
		t.Helper()
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		values := map[string]any{
			"tenant_id": s.Res.TenantID, "store_id": s.Res.StoreID,
			"kind": "item", "business_date": "2026-10-02", "order_id": id(changed["id"]),
			"order_item_id": id(firstLine(changed)["id"]), "checkout_id": changed["checkout_id"],
			"settings_snapshot_id": nil, "slot": 1, "position_id": s.position,
			"staff_id": s.Res.OwnerID, "method": "cash", "paid_amount": "1", "commission_paid_amount": "1",
			"ratio": 100, "category_ratio": 100, "amount": "1", "commission_amount": "1",
			"reverses_id": source, "void_id": nil, "aftersale_id": nil, "staff_change_id": nil, "member": false,
		}
		for key, value := range overrides {
			values[key] = value
		}
		query := `INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,commission_paid_amount,ratio,category_ratio,amount,commission_amount,reverses_id,void_id,aftersale_id,staff_change_id,member)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`
		_, err := tx.Exec(context.Background(), query, values["tenant_id"], values["store_id"], values["kind"], values["business_date"], values["order_id"], values["order_item_id"], values["checkout_id"], values["settings_snapshot_id"], values["slot"], values["position_id"], values["staff_id"], values["method"], values["paid_amount"], values["commission_paid_amount"], values["ratio"], values["category_ratio"], values["amount"], values["commission_amount"], values["reverses_id"], values["void_id"], values["aftersale_id"], values["staff_change_id"], values["member"])
		dbtest.Rollback(t, tx)
		voidState(t, err, state)
	}
	refused(map[string]any{}, "23514")
	refused(map[string]any{"void_id": voidID, "staff_change_id": changeID}, "23514")
	refused(map[string]any{"aftersale_id": id(aftersale["id"]), "staff_change_id": changeID}, "23514")

	for _, sql := range []string{"UPDATE order_staff_changes SET business_date=business_date", "DELETE FROM order_staff_changes"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		dbtest.Rollback(t, tx)
		voidState(t, err, "42501")
	}

	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO order_staff_changes (tenant_id,store_id,order_id,order_item_id,business_date,before_staff,after_staff,changed_by)
VALUES ($1,$2,$3,$4,'2026-10-02','[]','[]',$5)`, s.Res.TenantID, s.Res.StoreID, id(changed["id"]), id(firstLine(other)["id"]), s.Res.OwnerID)
	dbtest.Rollback(t, tx)
	voidState(t, err, "23503")

	if changeID == uuid.Nil {
		t.Fatal("missing staff change")
	}
}

func TestOrderStaffChangeViewKeepsEmptySlots(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	_, line := s.twoSlotItem("空岗位")
	line.Staff = []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}
	o := s.paid(s.customerOrder(line), payment("cash", "100"))
	replacement, _ := s.Hire("空岗位接替", "075", "settled-slots")
	s.SetNow(dayAt("2026-10-02"))
	after := s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: replacement}), 200)
	change := after["staff_changes"].([]any)[0].(map[string]any)
	for _, key := range []string{"before", "after"} {
		slots := change[key].([]any)
		if len(slots) != 2 {
			t.Fatalf("%s = %v", key, slots)
		}
		if slots[0].(map[string]any)["slot"] != float64(1) || slots[1].(map[string]any)["slot"] != float64(2) {
			t.Fatalf("%s slot order = %v", key, slots)
		}
		if slots[1].(map[string]any)["staff_id"] != nil {
			t.Fatalf("%s empty slot should keep null: %v", key, slots)
		}
	}
	if change["before"].([]any)[0].(map[string]any)["staff_id"] != s.Res.OwnerID.String() || change["after"].([]any)[0].(map[string]any)["staff_id"] != replacement.String() {
		t.Fatalf("change = %v", change)
	}
}

func TestOrderStaffChangeMigrationDown(t *testing.T) {
	s := open(t)
	s.SetNow(dayAt("2026-10-01"))
	o := s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	replacement, _ := s.Hire("回滚接替", "076", "settled-down")
	s.SetNow(dayAt("2026-10-02"))
	s.call("set_paid_item_staff", paidStaffInput(id(o["id"]), id(firstLine(o)["id"]), orders.OrderItemStaffInput{Slot: 1, StaffID: replacement}), 200)
	dbtest.MigrateDownTo(t, s.Pool, 58)
	err := db.MigrateDown(context.Background(), s.Pool)
	if err == nil || !strings.Contains(err.Error(), "无法回滚 00058") {
		t.Fatalf("down error = %v", err)
	}
	if version, err := db.MigrationVersion(context.Background(), s.Pool); err != nil || version != 58 {
		t.Fatalf("version = %d, err = %v", version, err)
	}
}
