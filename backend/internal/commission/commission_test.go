package commission_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/commission"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func today(t *testing.T, s *optest.Shop) time.Time {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	d, err := settings.BusinessDate(context.Background(), dbgen.New(tx), s.Res.StoreID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	return d
}

func emptyScheme() map[string]any {
	return map[string]any{"rules": []any{}, "overrides": []any{}, "excluded_staff": []any{}}
}
func rule(source string) map[string]any {
	return map[string]any{"source": source, "base": "performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{}}
}
func save(t *testing.T, s *optest.Shop, day time.Time, scheme any) optest.Result {
	t.Helper()
	r := s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": day.Format(time.DateOnly), "scheme": scheme})
	if r.Status != 200 {
		t.Fatal(r)
	}
	return r
}
func reject(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %s %v, want %d %s", r.Status, r.Detail(), r.Body, status, detail)
	}
}

func TestCommissionVersions(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s)
	first := save(t, s, d, emptyScheme())
	scheme := emptyScheme()
	scheme["rules"] = []any{rule("sale"), rule("item")}
	second := save(t, s, d, scheme)
	if first.Body["version"] != float64(1) || second.Body["version"] != float64(2) {
		t.Fatal(first, second)
	}
	if second.Body["scheme"].(map[string]any)["rules"].([]any)[0].(map[string]any)["source"] != "item" {
		t.Fatal(second)
	}
	future := save(t, s, d.AddDate(0, 0, 2), emptyScheme())
	caller := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			old := ch.Call(t, caller, "get_commission_scheme", map[string]any{"version": 1})
			if old.Status != 200 || !reflect.DeepEqual(old.Body, first.Body) {
				t.Fatal(old, first)
			}
			byDate := ch.Call(t, caller, "get_commission_scheme", map[string]any{"date": d.Format(time.DateOnly)})
			if byDate.Status != 200 || !reflect.DeepEqual(byDate.Body, second.Body) {
				t.Fatal(byDate, second)
			}
			later := ch.Call(t, caller, "get_commission_scheme", map[string]any{"date": d.AddDate(0, 0, 5).Format(time.DateOnly)})
			if later.Status != 200 || !reflect.DeepEqual(later.Body, future.Body) {
				t.Fatal(later)
			}
			page := ch.Call(t, caller, "list_commission_scheme_versions", map[string]any{"limit": 2})
			if page.Status != 200 {
				t.Fatal(page)
			}
			rows := page.Body["versions"].([]any)
			if len(rows) != 2 || rows[0].(map[string]any)["version"] != float64(3) || rows[1].(map[string]any)["version"] != float64(2) {
				t.Fatal(page)
			}
			if rows[0].(map[string]any)["saved_by"].(map[string]any)["name"] != "王老板" {
				t.Fatal(page)
			}
			if _, ok := rows[0].(map[string]any)["scheme"]; ok {
				t.Fatal(page)
			}
			tail := ch.Call(t, caller, "list_commission_scheme_versions", map[string]any{"before_version": 2})
			if tail.Status != 200 || len(tail.Body["versions"].([]any)) != 1 || tail.Body["versions"].([]any)[0].(map[string]any)["version"] != float64(1) {
				t.Fatal(tail)
			}
			reject(t, ch.Call(t, caller, "get_commission_scheme", map[string]any{"date": d.AddDate(0, 0, -1).Format(time.DateOnly)}), 404, "该营业日没有执行的提成方案")
			reject(t, ch.Call(t, caller, "get_commission_scheme", map[string]any{"version": 99}), 404, "没有这个提成方案版本")
			for _, args := range []map[string]any{{}, {"version": 1, "date": d.Format(time.DateOnly)}} {
				reject(t, ch.Call(t, caller, "get_commission_scheme", args), 409, "请且只按版本号或营业日之一查询提成方案")
			}
		})
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var before, after []byte
	if err := tx.QueryRow(context.Background(), "SELECT before,after FROM audit_log WHERE target_id=$1 AND target_kind='commission_scheme'", uuid.MustParse(first.Body["id"].(string))).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatal(string(before))
	}
	var got map[string]any
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first.Body) {
		t.Fatal(got, first)
	}
	if err := tx.QueryRow(context.Background(), "SELECT before,after FROM audit_log WHERE target_id=$1 AND action='save_commission_scheme'", uuid.MustParse(second.Body["id"].(string))).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(before, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first.Body) {
		t.Fatal(got, first)
	}
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, second.Body) {
		t.Fatal(got, second)
	}
	dbtest.Rollback(t, tx)
}

func TestCommissionDates(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s)
	args := func(day time.Time) map[string]any {
		return map[string]any{"effective_from": day.Format(time.DateOnly), "scheme": emptyScheme()}
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", args(d.AddDate(0, 0, -1))), 409, "提成方案生效日期不能早于今天（"+d.Format(time.DateOnly)+"）")
	save(t, s, d.AddDate(0, 0, 2), emptyScheme())
	reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", args(d.AddDate(0, 0, 1))), 409, "提成方案生效日期不能早于最新版本（"+d.AddDate(0, 0, 2).Format(time.DateOnly)+"）")
	save(t, s, d.AddDate(0, 0, 2), emptyScheme())
}

func TestCommissionPermissionsAndAI(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s)
	owner := s.MustCaller(s.Owner)
	args := map[string]any{"effective_from": d.Format(time.DateOnly), "scheme": emptyScheme()}
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "commission_scheme_versions", "audit_log")
	r := s.AI(t, owner, "save_commission_scheme", args)
	if !errors.Is(r.Err, ops.ErrDraftRequired) {
		t.Fatal(r)
	}
	if got := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "commission_scheme_versions", "audit_log"); got != before {
		t.Fatal("AI changed data")
	}
	caller := owner
	caller.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		for _, op := range []string{"save_commission_scheme", "list_commission_scheme_versions", "get_commission_scheme"} {
			input := map[string]any{}
			if op == "save_commission_scheme" {
				input = args
			}
			if op == "get_commission_scheme" {
				input = map[string]any{"version": 1}
			}
			reject(t, ch.Call(t, caller, op, input), 403, "permission denied")
		}
	}
	save(t, s, d, emptyScheme())
	caller.Permissions[perm.AuditLogView] = ops.Grant{}
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "list_audit_log", map[string]any{})
		if r.Status != 200 {
			t.Fatal(r)
		}
		for _, entry := range r.Body["entries"].([]any) {
			if entry.(map[string]any)["target_kind"] == "commission_scheme" {
				t.Fatal("scheme leaked in audit", r)
			}
		}
	}
}

func TestCommissionVersionCutoff(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s)
	first := save(t, s, d, emptyScheme())
	cutoff, err := time.Parse(time.RFC3339Nano, first.Body["saved_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	save(t, s, d, emptyScheme())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	row, err := commission.VersionInForce(context.Background(), q, s.Res.StoreID, d, &cutoff)
	if err != nil || row.Version != 1 {
		t.Fatal(row, err)
	}
	prior := cutoff.Add(-time.Second)
	_, err = commission.VersionInForce(context.Background(), q, s.Res.StoreID, d, &prior)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}

func TestCommissionSaveLock(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if err := dbgen.New(tx).LockCommissionScheme(context.Background(), s.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 2)
	for range 2 {
		go func() {
			done <- s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": d.Format(time.DateOnly), "scheme": emptyScheme()})
		}()
	}
	dbtest.WaitForLockWait(t, s.Pool)
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	a, b := optest.WaitResult(t, done, 200), optest.WaitResult(t, done, 200)
	if fmt.Sprint(a.Body["version"], b.Body["version"]) != "1 2" && fmt.Sprint(a.Body["version"], b.Body["version"]) != "2 1" {
		t.Fatal(a, b)
	}
}

func TestCommissionAllowedBases(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s)
	for _, base := range []string{"line_price", "paid", "performance", "cash_performance", "card_performance"} {
		sc := emptyScheme()
		r := rule("item")
		r["base"] = base
		sc["rules"] = []any{rule("sale"), rule("labor"), r, rule("consume")}
		got := save(t, s, d, sc)
		rules := got.Body["scheme"].(map[string]any)["rules"].([]any)
		for i, source := range []string{"item", "consume", "labor", "sale"} {
			if rules[i].(map[string]any)["source"] != source {
				t.Fatal(got)
			}
		}
	}
}
