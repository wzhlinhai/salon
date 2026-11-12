package orders_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) commissionScheme(percent string) map[string]any {
	s.t.Helper()
	return s.call("save_commission_scheme", map[string]any{"effective_from": settings.Date(time.Now()).Format(time.DateOnly), "scheme": map[string]any{"rules": []any{map[string]any{"source": "item", "base": "paid", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": percent}}}}, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
}
func commissionArgs(now time.Time) map[string]any {
	day := settings.Date(now).Format(time.DateOnly)
	return map[string]any{"date_from": day, "date_to": day}
}
func TestCommissionReadHistory(t *testing.T) {
	s := open(t)
	s.performanceSetting("performance_ratio_card_bonus", 100)
	s.performanceSetting("performance_ratio_red_packet", 100)
	scheme := s.commissionScheme("10")
	line := s.performanceLine(70, 30)
	card := s.paymentCard(s.paymentType("提成卡", nil), "30", "30")
	packet := s.redPacket(s.customer, "20")
	o := s.paid(s.customerOrder(line), payment("cash", "20"), cardPayment(card, "60"), packetPayment(packet, "20"))
	first := s.call("list_commissions", commissionArgs(s.Now()), 200)
	if first["total"] != "10.00" || len(first["entries"].([]any)) != 2 {
		t.Fatal(first)
	}
	for _, v := range first["entries"].([]any) {
		c := v.(map[string]any)
		e := c["explanation"].(map[string]any)
		origin := e["origin"].(map[string]any)
		want := "7.00"
		base := "70.00"
		if origin["slot"] == float64(2) {
			want = "3.00"
			base = "30.00"
		}
		if c["amount"] != want || c["source"] != "item" || c["business_date"] != commissionArgs(s.Now())["date_from"] || c["reversal"] != false || origin["order_id"] != o["id"] || origin["order_item_id"] != firstLine(o)["id"] || origin["checkout_id"] != o["checkout_id"] || origin["item"].(map[string]any)["id"] != line.ItemID.String() || origin["position"].(map[string]any)["id"] != s.position.String() || origin["position"].(map[string]any)["name"] != "大工" || e["base"].(map[string]any)["amount"] != base || e["base"].(map[string]any)["kind"] != "paid" || e["version"].(map[string]any)["id"] != scheme["id"] || e["version"].(map[string]any)["version"] != float64(1) || e["version"].(map[string]any)["effective_from"] != commissionArgs(s.Now())["date_from"] || e["match"].(map[string]any)["cell"].(map[string]any)["percent"] != "10.00" || e["member"] != true || e["designated"] != true || len(e["entries"].([]any)) != 4 {
			t.Fatal(c)
		}
		for _, entry := range e["entries"].([]any) {
			pe := entry.(map[string]any)
			if pe["id"] == nil || pe["method"] == nil || pe["paid_amount"] == nil || pe["ratio"] == nil || pe["amount"] == nil {
				t.Fatal(pe)
			}
		}
	}
	s.commissionScheme("20")
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionArgs(s.Now()))
		if got.Status != 200 || !reflect.DeepEqual(got.Body, first) {
			t.Fatal(ch.Name, got, first)
		}
	}
	s.paid(s.customerOrder(line), payment("cash", "100"))
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionArgs(s.Now()))
		if got.Status != 200 || got.Body["total"] != "30.00" || len(got.Body["entries"].([]any)) != 4 {
			t.Fatal(got)
		}
		raw, err := json.Marshal(got.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "customer") || strings.Contains(string(raw), "phone") {
			t.Fatal(string(raw))
		}
	}
	example, err := json.Marshal(first["entries"].([]any)[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("worked example: %s", example)
}
func TestCommissionReadScopePaging(t *testing.T) {
	s := open(t)
	s.commissionScheme("10")
	mine, token := s.Hire("本人", "098", "commission", s.Role("发型师"))
	line := s.performanceLine(nil, nil)
	line.Staff[0].StaffID = mine
	s.paid(s.customerOrder(line), payment("cash", "100"))
	s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	owner, self := s.MustCaller(s.Owner), s.MustCaller(token)
	denied := owner
	denied.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			args := commissionArgs(s.Now())
			all := ch.Call(t, owner, "list_commissions", args)
			if all.Status != 200 || len(all.Body["entries"].([]any)) != 3 || all.Body["total"] != "20.00" {
				t.Fatal(all)
			}
			own := ch.Call(t, self, "list_commissions", args)
			if own.Status != 200 || len(own.Body["entries"].([]any)) != 1 || own.Body["total"] != "5.00" || own.Body["entries"].([]any)[0].(map[string]any)["staff"].(map[string]any)["id"] != mine.String() {
				t.Fatal(own)
			}
			args["staff_id"] = s.employee
			filtered := ch.Call(t, owner, "list_commissions", args)
			if filtered.Status != 200 || len(filtered.Body["entries"].([]any)) != 1 || filtered.Body["total"] != "5.00" {
				t.Fatal(filtered)
			}
			filtered = ch.Call(t, self, "list_commissions", args)
			if filtered.Status != 200 || len(filtered.Body["entries"].([]any)) != 0 || filtered.Body["total"] != "0.00" {
				t.Fatal(filtered)
			}
			args = commissionArgs(s.Now())
			args["source"] = "sale"
			filtered = ch.Call(t, owner, "list_commissions", args)
			if filtered.Status != 200 || len(filtered.Body["entries"].([]any)) != 0 || filtered.Body["total"] != "0.00" {
				t.Fatal(filtered)
			}
			args["source"] = "item"
			filtered = ch.Call(t, owner, "list_commissions", args)
			if filtered.Status != 200 || !reflect.DeepEqual(all.Body, filtered.Body) {
				t.Fatal(filtered)
			}
			args = commissionArgs(s.Now())
			args["limit"] = 1
			page := ch.Call(t, owner, "list_commissions", args)
			if page.Status != 200 || len(page.Body["entries"].([]any)) != 1 || page.Body["total"] != all.Body["total"] || !reflect.DeepEqual(page.Body["totals"], all.Body["totals"]) {
				t.Fatal(page)
			}
			args["before_id"] = page.Body["entries"].([]any)[0].(map[string]any)["id"]
			delete(args, "limit")
			tail := ch.Call(t, owner, "list_commissions", args)
			if tail.Status != 200 || !reflect.DeepEqual(tail.Body["entries"], all.Body["entries"].([]any)[1:]) || tail.Body["total"] != all.Body["total"] {
				t.Fatal(tail)
			}
			for _, tc := range []struct{ from, to, detail string }{{"2026-01-01", "2026-04-04", "查询范围最多 92 天"}, {"2026-02-01", "2026-01-01", "开始营业日不能晚于结束营业日"}} {
				got := ch.Call(t, owner, "list_commissions", map[string]any{"date_from": tc.from, "date_to": tc.to})
				if got.Status != 409 || got.Detail() != tc.detail {
					t.Fatal(got)
				}
			}
			got := ch.Call(t, owner, "list_commissions", map[string]any{"date_from": "2026-01-01", "date_to": "2026-04-03"})
			if got.Status != 200 {
				t.Fatal(got)
			}
			got = ch.Call(t, denied, "list_commissions", commissionArgs(s.Now()))
			if got.Status != 403 || got.Detail() != "permission denied" {
				t.Fatal(got)
			}
		})
	}
}

func TestExplainCommission(t *testing.T) {
	s := open(t)
	s.commissionScheme("10")
	mine, token := s.Hire("本人", "098", "explain-commission", s.Role("发型师"))
	ownLine := s.performanceLine(100)
	ownLine.Staff[0].StaffID = mine
	s.paid(s.customerOrder(ownLine), payment("cash", "100"))
	other := s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	list := s.call("list_commissions", commissionArgs(s.Now()), 200)
	var ownID, otherID string
	for _, raw := range list["entries"].([]any) {
		entry := raw.(map[string]any)
		staff := entry["staff"].(map[string]any)["id"]
		if staff == mine.String() {
			ownID = entry["id"].(string)
		} else {
			otherID = entry["id"].(string)
		}
	}
	if ownID == "" || otherID == "" {
		t.Fatal(list)
	}
	owner, self := s.MustCaller(s.Owner), s.MustCaller(token)
	denied := self
	denied.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			got := ch.Call(t, owner, "explain_commission", map[string]any{"id": otherID})
			if got.Status != 200 || got.Body["id"] != otherID {
				t.Fatal(got)
			}
			origin := got.Body["explanation"].(map[string]any)["origin"].(map[string]any)
			if origin["order_id"] != other["id"] || origin["order_item_id"] != firstLine(other)["id"] || got.Body["source"] != "item" {
				t.Fatal(got)
			}
			raw, err := json.Marshal(got.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "customer") || strings.Contains(string(raw), "phone") {
				t.Fatal(string(raw))
			}
			ownGot := ch.Call(t, self, "explain_commission", map[string]any{"id": ownID})
			if ownGot.Status != 200 || ownGot.Body["id"] != ownID {
				t.Fatal(ownGot)
			}
			refused := ch.Call(t, self, "explain_commission", map[string]any{"id": otherID})
			if refused.Status != 404 || !strings.Contains(refused.Detail(), "没有这笔提成") {
				t.Fatal(refused)
			}
			forbidden := ch.Call(t, denied, "explain_commission", map[string]any{"id": ownID})
			if forbidden.Status != 403 || forbidden.Detail() != "permission denied" {
				t.Fatal(forbidden)
			}
			missing := ch.Call(t, owner, "explain_commission", map[string]any{"id": uuid.NewString()})
			if missing.Status != 404 || !strings.Contains(missing.Detail(), "没有这笔提成") {
				t.Fatal(missing)
			}
		})
	}
}
func TestCommissionReadReversal(t *testing.T) {
	s := open(t)
	s.commissionScheme("10")
	o := s.paid(s.customerOrder(s.performanceLine(70, 30)), payment("cash", "100"))
	s.commissionScheme("90")
	originalNow := s.Now()
	day := settings.Date(originalNow).AddDate(0, 0, 1)
	s.SetNow(day.Add(8 * time.Hour))
	s.call("void_order", voidInput(o), 200)
	for _, ch := range s.Channels() {
		for _, onlyVoid := range []bool{false, true} {
			args := commissionArgs(originalNow)
			args["date_to"] = day.Format(time.DateOnly)
			want := "0.00"
			n := 4
			if onlyVoid {
				args["date_from"] = day.Format(time.DateOnly)
				want = "-10.00"
				n = 2
			}
			got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", args)
			if got.Status != 200 || got.Body["total"] != want || len(got.Body["entries"].([]any)) != n || got.Body["totals"].([]any)[0].(map[string]any)["amount"] != want {
				t.Fatal(got)
			}
			for _, v := range got.Body["entries"].([]any)[:2] {
				c := v.(map[string]any)
				e := c["explanation"].(map[string]any)
				if c["reversal"] != true || !strings.HasPrefix(c["amount"].(string), "-") || c["business_date"] != day.Format(time.DateOnly) || e["version"].(map[string]any)["version"] != float64(1) || e["reverses"] == nil || e["origin"].(map[string]any)["void_id"] == nil {
					t.Fatal(c)
				}
			}
		}
	}
}
func TestCommissionReadQueryCount(t *testing.T) {
	s := open(t)
	s.commissionScheme("10")
	s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	for _, ch := range app.Channels() {
		var counts []int64
		for i := range 2 {
			if i == 1 {
				s.paid(s.customerOrder(s.performanceLine(70, 30), s.performanceLine(70, 30)), payment("cash", "200"))
			}
			counter.all.Store(0)
			got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionArgs(s.Now()))
			if got.Status != 200 || len(got.Body["entries"].([]any)) == 0 {
				t.Fatal(got)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, counts)
		}
	}
}

func TestExplainCommissionQueryCount(t *testing.T) {
	s := open(t)
	s.commissionScheme("10")
	s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	list := s.call("list_commissions", commissionArgs(s.Now()), 200)
	first := list["entries"].([]any)[0].(map[string]any)
	commissionID := first["id"].(string)

	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		var counts []int64
		for i := range 2 {
			if i == 1 {
				s.paid(s.customerOrder(s.performanceLine(70, 30), s.performanceLine(70, 30)), payment("cash", "200"))
			}
			counter.all.Store(0)
			got := ch.Call(t, caller, "explain_commission", map[string]any{"id": commissionID})
			if got.Status != 200 || got.Body["id"] != commissionID {
				t.Fatal(ch.Name, got)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, "SQL query count grew with commission rows", counts)
		}
	}
}

func TestExplainCommissionSettledMonth(t *testing.T) {
	s := open(t)
	day := settings.Date(time.Now())
	month := time.Date(day.Year(), day.Month()-2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Pool.Exec(t.Context(), "UPDATE stores SET created_at=$1", month.AddDate(0, 0, 15)); err != nil {
		t.Fatal(err)
	}
	businessDay := month.AddDate(0, 0, 14)
	s.SetNow(time.Date(businessDay.Year(), businessDay.Month(), businessDay.Day(), 12, 0, 0, 0, settings.Location))
	s.call("save_commission_scheme", map[string]any{"effective_from": businessDay.Format(time.DateOnly), "scheme": map[string]any{
		"rules":     []any{map[string]any{"source": "item", "base": "paid", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10"}}}},
		"overrides": []any{}, "excluded_staff": []any{},
	}}, 200)
	order := s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	args := map[string]any{"date_from": businessDay.Format(time.DateOnly), "date_to": businessDay.Format(time.DateOnly)}
	list := s.call("list_commissions", args, 200)
	first := list["entries"].([]any)[0].(map[string]any)
	commissionID, amount := first["id"].(string), first["amount"].(string)
	if amount != "10.00" {
		t.Fatalf("commission = %v", first)
	}

	s.SetNow(time.Now())
	s.call("lock_payroll_month", map[string]any{"month": month.Format("2006-01")}, 200)
	// 结算后让现算结果变成 20.00；解释仍应读取锁定时保存的 10.00。
	if _, err := s.Pool.Exec(t.Context(), "UPDATE performance_entries SET paid_amount=200, amount=200 WHERE order_id=$1", order["id"]); err != nil {
		t.Fatal(err)
	}
	s.SetNow(time.Time{})
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "explain_commission", map[string]any{"id": commissionID})
		if got.Status != 200 || got.Body["id"] != commissionID || got.Body["amount"] != amount {
			t.Fatal(ch.Name, got)
		}
	}
}
func TestCheckoutCommissionMember(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{{"no card", false}, {"active", true}, {"today", true}, {"expired", false}, {"refunded", false}, {"other owner", false}, {"other store", false}, {"count empty", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			line := s.performanceLine(70, 30)
			if tc.name != "no card" {
				typeInput := map[string]any{"name": "会员", "kind": "stored_value", "validity_kind": "none"}
				switch tc.name {
				case "count empty":
					typeInput = map[string]any{"name": "空次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": line.ItemID, "paid_count": 1, "gift_count": 0, "unit_price": "0"}}}
				case "today", "expired":
					typeInput["validity_kind"], typeInput["validity_days"] = "days_after_open", 1
				}
				ct := s.call("create_card_type", typeInput, 200)
				if tc.name == "today" || tc.name == "expired" {
					opened := settings.Date(s.Now()).AddDate(0, 0, -1)
					if tc.name == "expired" {
						opened = opened.AddDate(0, 0, -1)
					}
					s.SetNow(time.Date(opened.Year(), opened.Month(), opened.Day(), 12, 0, 0, 0, settings.Location))
				}
				c := s.paymentCard(ct, "0", "0")
				if tc.name == "today" || tc.name == "expired" {
					s.SetNow(time.Time{})
				}
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				switch tc.name {
				case "refunded":
					dbtest.Exec(t, tx, "UPDATE cards SET status='refunded' WHERE id=$1", c["id"])
				case "other owner":
					other := s.call("create_customer", map[string]any{"name": "别人的顾客"}, 200)["customer"].(map[string]any)["id"]
					dbtest.Exec(t, tx, "UPDATE cards SET customer_id=$2 WHERE id=$1", c["id"], other)
				case "other store":
					store, err := dbgen.New(tx).CreateStore(t.Context(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "外店"})
					if err != nil {
						t.Fatal(err)
					}
					dbtest.Exec(t, tx, "UPDATE cards SET store_id=$2 WHERE id=$1", c["id"], store.ID)
				}
				dbtest.Commit(t, tx)
			}
			o := s.paid(s.customerOrder(line), payment("cash", "100"))
			s.void(o)
			entries := withoutSale(s.performanceList()["entries"].([]any))
			if len(entries) != 4 {
				t.Fatal(entries)
			}
			for _, v := range entries {
				e := v.(map[string]any)
				if e["member"] != tc.want {
					t.Fatal(tc.name, e)
				}
			}
		})
	}
}
func TestPerformanceMemberConstraintAndDown(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	c := s.paymentCard(s.paymentType("会员校验", nil), "1", "0")
	entries := s.performanceList()["entries"].([]any)
	for _, v := range entries {
		e := v.(map[string]any)
		if e["kind"] == "sale" {
			s.cloneRefused("performance_entries", e["id"], map[string]any{"member": false}, "23514")
		} else {
			s.cloneRefused("performance_entries", e["id"], map[string]any{"member": nil}, "23514")
		}
	}
	if c["id"] == nil {
		t.Fatal(c)
	}
	dbtest.MigrateDownTo(t, s.Pool, 32)
	var n int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM information_schema.columns WHERE table_name='performance_entries' AND column_name='member'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestCommissionLinePriceFacts(t *testing.T) {
	s := open(t)
	sc := map[string]any{"rules": []any{map[string]any{"source": "item", "base": "line_price", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10"}}}}, "overrides": []any{}, "excluded_staff": []any{}}
	s.call("save_commission_scheme", map[string]any{"effective_from": settings.Date(time.Now()).Format(time.DateOnly), "scheme": sc}, 200)
	line := s.performanceLine(70, 30)
	o := s.paid(s.customerOrder(line), payment("cash", "100"))
	// 同一行加门店未分配记录，确保分母包含它，而列表不产生门店提成。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO performance_entries(tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,method,paid_amount,ratio,amount,member)
 SELECT tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,'cash',100,100,100,member FROM performance_entries WHERE order_id=$1 LIMIT 1`, o["id"])
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionArgs(s.Now()))
		if got.Status != 200 || len(got.Body["entries"].([]any)) != 2 || got.Body["total"] != "5.00" {
			t.Fatal(got)
		}
		for _, v := range got.Body["entries"].([]any) {
			c := v.(map[string]any)
			b := c["explanation"].(map[string]any)["base"].(map[string]any)
			want, paid := "35.00", "70.00"
			if c["explanation"].(map[string]any)["origin"].(map[string]any)["slot"] == float64(2) {
				want, paid = "15.00", "30.00"
			}
			if b["amount"] != want || b["line_price"] != "100.00" || b["unit_paid"] != paid || b["line_paid"] != "200.00" {
				t.Fatal(c)
			}
		}
	}
}

func TestCommissionAllSources(t *testing.T) {
	s := open(t)
	rules := []any{}
	for _, source := range []string{"item", "consume", "labor", "sale"} {
		rules = append(rules, map[string]any{"source": source, "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10"}}})
	}
	s.call("save_commission_scheme", map[string]any{"effective_from": settings.Date(time.Now()).Format(time.DateOnly), "scheme": map[string]any{"rules": rules, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
	line := s.laborLine("10", nil)
	card := s.countCard(line, "count", 1, 0)
	packet := s.call("issue_red_packet", map[string]any{"customer_id": s.customer, "kind": "sold", "face_value": "100", "price": "20", "expires_on": "2999-01-01", "sales_staff_ids": []any{s.Res.OwnerID}, "payments": testPayments("20")}, 200)
	o := s.customerOrder(line)
	s.paid(o, countPayment(card, firstLine(o), "100"))
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionArgs(s.Now()))
		if got.Status != 200 || len(got.Body["entries"].([]any)) != 6 || got.Body["total"] != "23.00" {
			t.Fatal(got)
		}
		for _, v := range got.Body["entries"].([]any) {
			c := v.(map[string]any)
			e := c["explanation"].(map[string]any)
			origin := e["origin"].(map[string]any)
			if c["source"] == "sale" {
				if e["member"] != nil || e["designated"] != nil || origin["item"] != nil || origin["order_id"] != nil || (origin["red_packet_id"] != packet["id"] && origin["card_ledger_id"] == nil) {
					t.Fatal(c)
				}
			} else if e["member"] != true || origin["order_item_id"] != firstLine(o)["id"] || origin["item"].(map[string]any)["id"] != line.ItemID.String() {
				t.Fatal(c)
			}
		}
	}
}

func TestCommissionStoreTenantScope(t *testing.T) {
	s := open(t)
	s.commissionScheme("10")
	s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	store := s.otherStore()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO performance_entries SELECT (jsonb_populate_record(NULL::performance_entries,to_jsonb(e)||jsonb_build_object('id',uuidv7(),'store_id',$1::uuid))).* FROM performance_entries e LIMIT 1`, store)
	dbtest.Commit(t, tx)
	foreign := optest.OpenShop(t)
	caller := foreign.MustCaller(foreign.Owner)
	caller.StoreID = s.Res.StoreID
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionArgs(s.Now()))
		if got.Status != 200 || len(got.Body["entries"].([]any)) != 1 || got.Body["total"] != "10.00" {
			t.Fatal(got)
		}
		got = ch.Call(t, caller, "list_commissions", commissionArgs(s.Now()))
		if got.Status != 200 || len(got.Body["entries"].([]any)) != 0 || got.Body["total"] != "0.00" {
			t.Fatal(got)
		}
	}
}
