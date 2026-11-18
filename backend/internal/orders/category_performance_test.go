package orders_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) itemWithCategory(name string, category string, slots int, labor any, shares ...int) orders.OrderItemInput {
	s.t.Helper()
	in := s.itemInput(name, slots)
	in["category_id"] = category
	in["labor_fee_amount"] = labor
	positions := []any{}
	for i := range slots {
		p := map[string]any{"position_id": s.position, "required": false}
		if len(shares) > 0 {
			p["share_percent"] = shares[i]
		}
		positions = append(positions, p)
	}
	in["positions"] = positions
	item := s.call("create_item", in, 200)
	staff := []orders.OrderItemStaffInput{}
	for i := 1; i <= slots; i++ {
		id := s.Res.OwnerID
		if i == 2 {
			id = s.employee
		}
		staff = append(staff, orders.OrderItemStaffInput{Slot: int16(i), StaffID: id})
	}
	return orders.OrderItemInput{ItemID: id(item["id"]), Staff: staff}
}

func (s *fixture) performanceEntries(kind string) []map[string]any {
	s.t.Helper()
	out := []map[string]any{}
	for _, raw := range s.performanceList()["entries"].([]any) {
		entry := raw.(map[string]any)
		if entry["kind"] == kind {
			out = append(out, entry)
		}
	}
	return out
}

func TestCategoryRatioSnapshotExactAmounts(t *testing.T) {
	s := open(t)
	category := s.call("create_item_category", map[string]any{"name": "烫染", "performance_ratio": 100}, 200)
	in := s.itemInput("类别项目", 1)
	in["category_id"] = category["id"]
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}}
	order := s.customerOrder(line)
	oldLine := id(lines(order)[0].(map[string]any)["id"])
	s.call("update_item_category", map[string]any{"id": category["id"], "name": "烫染", "performance_ratio": 90, "active": true}, 200)
	added := s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(order["id"]), Item: line}, 200)
	newLine := id(lines(added)[1].(map[string]any)["id"])
	s.paid(order, payment("cash", "200"))
	entries := s.performanceEntries("item")
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	byLine := map[uuid.UUID]map[string]any{}
	for _, entry := range entries {
		byLine[id(entry["order_item_id"])] = entry
	}
	if byLine[oldLine]["amount"] != "100.00" || byLine[oldLine]["category_ratio"] != float64(100) || byLine[newLine]["amount"] != "90.00" || byLine[newLine]["category_ratio"] != float64(90) {
		t.Fatal(byLine)
	}
}

func TestCategoryChangeSnapshot(t *testing.T) {
	s := open(t)
	firstCategory := s.call("create_item_category", map[string]any{"name": "剪发", "performance_ratio": 100}, 200)
	secondCategory := s.call("create_item_category", map[string]any{"name": "护理", "performance_ratio": 50}, 200)
	in := s.itemInput("改类别项目", 1)
	in["category_id"] = firstCategory["id"]
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}}
	order := s.customerOrder(line)
	oldLine := id(lines(order)[0].(map[string]any)["id"])
	edited := map[string]any{}
	for k, v := range in {
		edited[k] = v
	}
	edited["id"] = item["id"]
	edited["active"] = true
	edited["category_id"] = secondCategory["id"]
	s.call("update_item", edited, 200)
	added := s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(order["id"]), Item: line}, 200)
	newLine := id(lines(added)[1].(map[string]any)["id"])
	s.paid(order, payment("cash", "200"))
	entries := s.performanceEntries("item")
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	amounts := map[uuid.UUID]string{}
	ratios := map[uuid.UUID]float64{}
	for _, entry := range entries {
		id := id(entry["order_item_id"])
		amounts[id] = entry["amount"].(string)
		ratios[id] = entry["category_ratio"].(float64)
	}
	if amounts[oldLine] != "100.00" || ratios[oldLine] != 100 || amounts[newLine] != "50.00" || ratios[newLine] != 50 {
		t.Fatal(amounts, ratios)
	}
}

func TestCategoryRatioSingleRounding(t *testing.T) {
	s := open(t)
	category := s.call("create_item_category", map[string]any{"name": "九折类别", "performance_ratio": 90}, 200)
	in := s.itemInput("现金项目", 1)
	in["category_id"] = category["id"]
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}}
	order := s.customerOrder(line)
	s.paid(order, payment("cash", "100"))
	entries := s.performanceEntries("item")
	if len(entries) != 1 || entries[0]["amount"] != "90.00" || entries[0]["ratio"] != float64(100) || entries[0]["category_ratio"] != float64(90) {
		t.Fatal(entries)
	}

	s.performanceSetting("performance_ratio_card_bonus", 50)
	odd := s.itemInput("奇数项目", 1)
	odd["category_id"] = category["id"]
	odd["price"] = "0.03"
	oddItem := s.call("create_item", odd, 200)
	card := s.paymentCard(s.paymentType("赠送金卡", nil), "0", "0.03")
	oddLine := orders.OrderItemInput{ItemID: id(oddItem["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}}
	oddOrder := s.customerOrder(oddLine)
	s.paid(oddOrder, cardPayment(card, "0.03"))
	entries = s.performanceEntries("item")
	var bonus map[string]any
	for _, entry := range entries {
		if entry["method"] == "card_bonus" {
			bonus = entry
		}
	}
	// 0.03 × 50% × 90% 单次四舍五入是 0.01；先分别四舍五入会得到 0.02。
	if bonus == nil || bonus["paid_amount"] != "0.03" || bonus["amount"] != "0.01" || bonus["ratio"] != float64(50) || bonus["category_ratio"] != float64(90) {
		t.Fatal(entries)
	}
}

func TestLaborAndSaleCategoryRatioStayFull(t *testing.T) {
	s := open(t)
	category := s.call("create_item_category", map[string]any{"name": "手工类别", "performance_ratio": 90}, 200)
	line := s.itemWithCategory("手工项目", category["id"].(string), 1, "10.00")
	order := s.customerOrder(line)
	s.paid(order, payment("cash", "100"))
	labor := s.performanceEntries("labor")
	if len(labor) != 1 || labor[0]["category_ratio"] != float64(100) || labor[0]["amount"] != "10.00" {
		t.Fatal(labor)
	}
	s.paymentCard(s.paymentType("销售卡", nil), "20", "0")
	sales := s.performanceEntries("sale")
	if len(sales) != 1 || sales[0]["category_ratio"] != float64(100) || sales[0]["ratio"] != float64(100) {
		t.Fatal(sales)
	}
}

func TestFullSplitWholeCell(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_split", "full")
	category := s.call("create_item_category", map[string]any{"name": "全额类别", "performance_ratio": 90}, 200)
	line := s.itemWithCategory("全额项目", category["id"].(string), 2, "10.00", 70, 30)
	order := s.customerOrder(line)
	itemLine := lines(order)[0].(map[string]any)
	s.paid(order, payment("cash", "100"))
	card := s.countCard(line, "count", 1, 0)
	countOrder := s.customerOrder(line)
	countLine := lines(countOrder)[0].(map[string]any)
	s.paid(countOrder, countPayment(card, countLine, "100"))

	items := s.performanceEntries("item")
	if len(items) != 2 || items[0]["paid_amount"] != "100.00" || items[1]["paid_amount"] != "100.00" || items[0]["amount"] != "90.00" || items[1]["amount"] != "90.00" {
		t.Fatal(items)
	}
	consume := s.performanceEntries("consume")
	if len(consume) != 2 || consume[0]["paid_amount"] != "100.00" || consume[1]["paid_amount"] != "100.00" || consume[0]["amount"] != "90.00" || consume[1]["amount"] != "90.00" {
		t.Fatal(consume)
	}
	labor := []map[string]any{}
	for _, entry := range s.performanceEntries("labor") {
		if entry["order_item_id"] == itemLine["id"] {
			labor = append(labor, entry)
		}
	}
	if len(labor) != 2 {
		t.Fatal(labor)
	}
	amounts := map[float64]string{}
	for _, entry := range labor {
		amounts[entry["slot"].(float64)] = entry["amount"].(string)
	}
	if amounts[1] != "7.00" || amounts[2] != "3.00" {
		t.Fatal(labor)
	}

	empty := s.checkoutLine()
	emptyOrder := s.customerOrder(empty)
	s.paid(emptyOrder, payment("cash", "100"))
	storeEntry := map[string]any{}
	for _, entry := range s.performanceEntries("item") {
		if entry["staff"] == nil {
			storeEntry = entry
		}
	}
	if storeEntry["paid_amount"] != "100.00" || storeEntry["amount"] != "100.00" {
		t.Fatal(storeEntry)
	}
}

func TestCommissionFullSplit(t *testing.T) {
	for _, base := range []string{"performance", "line_price"} {
		t.Run(base, func(t *testing.T) {
			s := open(t)
			s.performanceSetting("performance_split", "full")
			line := s.performanceLine(70, 30)
			s.call("save_commission_scheme", map[string]any{"effective_from": settings.Date(s.Now()).Format(settings.DateLayout), "scheme": map[string]any{"rules": []any{map[string]any{"source": "item", "base": base, "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10"}}}}, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
			s.paid(s.customerOrder(line), payment("cash", "100"))
			got := s.call("list_commissions", commissionArgs(s.Now()), 200)
			if got["total"] != "20.00" || len(got["entries"].([]any)) != 2 {
				t.Fatal(got)
			}
			for _, raw := range got["entries"].([]any) {
				entry := raw.(map[string]any)
				explanation := entry["explanation"].(map[string]any)
				baseView := explanation["base"].(map[string]any)
				if entry["amount"] != "10.00" || baseView["amount"] != "100.00" {
					t.Fatal(entry)
				}
				if base == "line_price" && (baseView["line_price"] != "100.00" || baseView["unit_paid"] != "100.00" || baseView["line_paid"] != "100.00") {
					t.Fatal(entry)
				}
			}
		})
	}
}

func TestCategoryMigrationDownRefusesCategoryPerformance(t *testing.T) {
	s := open(t)
	category := s.call("create_item_category", map[string]any{"name": "回滚比例", "performance_ratio": 90}, 200)
	in := s.itemInput("回滚比例项目", 1)
	in["category_id"] = category["id"]
	item := s.call("create_item", in, 200)
	line := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.Res.OwnerID}}}
	s.paid(s.customerOrder(line), payment("cash", "100"))
	dbtest.MigrateDownTo(t, s.Pool, 54)
	err := db.MigrateDown(context.Background(), s.Pool)
	if err == nil || !strings.Contains(err.Error(), "无法回滚 00054：存在类别比例不是 100 的业绩记录") {
		t.Fatal(err)
	}
}
