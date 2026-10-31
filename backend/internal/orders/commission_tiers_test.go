package orders_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func tierMonth() time.Time {
	day := settings.Date(time.Now())
	return time.Date(day.Year(), day.Month()+1, 10, 0, 0, 0, 0, day.Location())
}
func (s *fixture) tierScheme(mode, upper string) map[string]any {
	s.t.Helper()
	tiers := map[string]any{"mode": mode, "levels": []any{map[string]any{"from": "0.00", "percent": "10.00"}, map[string]any{"from": "10000.00", "percent": upper}}}
	return s.call("save_commission_scheme", map[string]any{"effective_from": settings.Date(time.Now()).Format(time.DateOnly), "scheme": map[string]any{"rules": []any{map[string]any{"source": "item", "base": "paid", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"tiers": tiers}}}}, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
}
func (s *fixture) tierCheckout(amount string, day time.Time, staff uuid.UUID) map[string]any {
	s.t.Helper()
	in := s.itemInput("阶梯"+uuid.NewString(), 0)
	in["price"] = amount
	in["level_prices"] = []any{}
	in["positions"] = []any{map[string]any{"position_id": s.position, "required": false, "share_percent": 100}}
	item := s.call("create_item", in, 200)
	o := s.customerOrder(orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: staff}}})
	err := db.InTenant(s.t.Context(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.CheckoutOrdersAtForTest(s.t.Context(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(checkoutInput(checkoutOrder(o, payment("cash", amount)))), day.Add(12*time.Hour))
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return o
}
func tierArgs(day time.Time) map[string]any {
	return map[string]any{"date_from": day.Format(time.DateOnly), "date_to": day.Format(time.DateOnly)}
}
func TestCommissionTierReadMonthAndScope(t *testing.T) {
	s := open(t)
	s.tierScheme("whole", "15.00")
	mine, token := s.Hire("阶梯本人", "097", "tiers", s.Role("发型师"))
	day := tierMonth()
	s.tierCheckout("6000", day, mine)
	s.tierCheckout("5000", day.AddDate(0, 0, 1), mine)
	s.tierCheckout("20000", day, s.Res.OwnerID)
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			day    time.Time
			amount string
		}{{day, "900.00"}, {day.AddDate(0, 0, 1), "750.00"}} {
			args := tierArgs(tc.day)
			got := ch.Call(t, s.MustCaller(token), "list_commissions", args)
			if got.Status != 200 || len(got.Body["entries"].([]any)) != 1 || got.Body["total"] != tc.amount || got.Body["totals"].([]any)[0].(map[string]any)["amount"] != tc.amount {
				t.Fatal(got)
			}
			c := got.Body["entries"].([]any)[0].(map[string]any)
			tier := c["explanation"].(map[string]any)["tier"].(map[string]any)
			if c["staff"].(map[string]any)["id"] != mine.String() || tier["cumulative"] != "11000.00" || tier["month"] != day.Format("2006-01") || c["amount"] != tc.amount {
				t.Fatal(c)
			}
			raw, err := json.Marshal(tier)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("whole list_commissions explanation.tier: %s", raw)
			args["staff_id"] = s.Res.OwnerID
			empty := ch.Call(t, s.MustCaller(token), "list_commissions", args)
			if empty.Status != 200 || len(empty.Body["entries"].([]any)) != 0 || empty.Body["total"] != "0.00" || empty.Body["totals"].([]any)[0].(map[string]any)["amount"] != "0.00" {
				t.Fatal(empty)
			}
		}
	}
}
func TestCommissionTierReadHistory(t *testing.T) {
	s := open(t)
	first := s.tierScheme("whole", "15.00")
	day := tierMonth()
	s.tierCheckout("6000", day, s.Res.OwnerID)
	s.tierCheckout("5000", day, s.Res.OwnerID)
	before := s.call("list_commissions", tierArgs(day), 200)
	s.tierScheme("whole", "30.00")
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", tierArgs(day))
		if got.Status != 200 || !reflect.DeepEqual(got.Body, before) {
			t.Fatal(got, before)
		}
	}
	s.tierCheckout("1000", day, s.Res.OwnerID)
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", tierArgs(day))
		if got.Status != 200 || len(got.Body["entries"].([]any)) != 3 || got.Body["total"] != "1950.00" {
			t.Fatal(got)
		}
		for _, entry := range got.Body["entries"].([]any) {
			c := entry.(map[string]any)
			e := c["explanation"].(map[string]any)
			if e["version"].(map[string]any)["id"] == first["id"] {
				if c["amount"] != "900.00" && c["amount"] != "750.00" {
					t.Fatal(c)
				}
			} else if c["amount"] != "300.00" || e["version"].(map[string]any)["version"] != float64(2) {
				t.Fatal(c)
			}
		}
	}
}
func TestCommissionTierReadProgressiveReversal(t *testing.T) {
	s := open(t)
	s.tierScheme("progressive", "15.00")
	day := tierMonth()
	original := s.tierCheckout("6000", day, s.Res.OwnerID)
	s.tierCheckout("6000", day.AddDate(0, 0, 1), s.Res.OwnerID)
	err := db.InTenant(t.Context(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.VoidOrderAtForTest(t.Context(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(voidInput(original)), day.AddDate(0, 0, 2).Add(12*time.Hour))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", tierArgs(day.AddDate(0, 0, 2)))
		if got.Status != 200 || len(got.Body["entries"].([]any)) != 1 || got.Body["total"] != "-700.00" {
			t.Fatal(got)
		}
		c := got.Body["entries"].([]any)[0].(map[string]any)
		tier := c["explanation"].(map[string]any)["tier"].(map[string]any)
		if tier["before"] != "12000.00" || tier["after"] != "6000.00" || len(tier["parts"].([]any)) != 2 {
			t.Fatal(tier)
		}
		raw, err := json.Marshal(tier)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("progressive reversal list_commissions explanation.tier: %s", raw)
		args := tierArgs(day)
		args["date_to"] = day.AddDate(0, 0, 2).Format(time.DateOnly)
		got = ch.Call(t, s.MustCaller(s.Owner), "list_commissions", args)
		if got.Status != 200 || got.Body["total"] != "600.00" {
			t.Fatal(got)
		}
	}
}
func TestCommissionTierReadQueryCount(t *testing.T) {
	s := open(t)
	s.tierScheme("whole", "15.00")
	day := tierMonth()
	s.tierCheckout("6000", day, s.Res.OwnerID)
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
				s.tierCheckout("5000", day.AddDate(0, 0, 1), s.Res.OwnerID)
			}
			counter.all.Store(0)
			got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", tierArgs(day))
			if got.Status != 200 || len(got.Body["entries"].([]any)) != 1 {
				t.Fatal(got)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, counts)
		}
	}
}
