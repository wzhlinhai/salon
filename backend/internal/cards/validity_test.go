package cards_test

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestValidityUsesBusinessDate(t *testing.T) {
	s := open(t)
	in := typeInput("三十天")
	in["validity_kind"] = "days_after_open"
	in["validity_days"] = 30
	ct := s.call("create_card_type", in, 200)
	s.SetNow(time.Date(2029, 12, 31, 12, 0, 0, 0, settings.Location))
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": "2030-01-01"}, 200)
	s.SetNow(time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location))
	opening := s.opening()
	opening["card_type_id"], opening["principal"], opening["bonus"] = ct["id"], "0", "0"
	out := s.call("open_card", opening, 200)
	if out["expires_on"] != "2030-01-31" {
		t.Fatal(out)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var day string
	if err := tx.QueryRow(context.Background(), "SELECT business_date::text FROM card_ledger WHERE card_id=$1", out["id"]).Scan(&day); err != nil || day != "2030-01-01" {
		t.Fatal(day, err)
	}
	dbtest.Rollback(t, tx)
}

func TestPointsUseBusinessDateRate(t *testing.T) {
	s := open(t)
	later := settings.Date(time.Now())
	earlier := later.AddDate(0, 0, -1)
	s.SetNow(earlier.AddDate(0, 0, -1).Add(12 * time.Hour))
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": earlier.Format(time.DateOnly)}, 200)
	s.call("change_setting", map[string]any{"key": "points_per_recharge_yuan", "value": 10, "effective_from": earlier.Format(time.DateOnly)}, 200)
	s.call("change_setting", map[string]any{"key": "points_per_recharge_yuan", "value": 1, "effective_from": later.Format(time.DateOnly)}, 200)
	s.SetNow(time.Date(later.Year(), later.Month(), later.Day(), 1, 0, 0, 0, settings.Location))
	opening := s.opening()
	opening["principal"], opening["bonus"] = "25.99", "0"
	out := s.call("open_card", opening, 200)
	if out["points"] != float64(2) {
		t.Fatalf("01:00 opening points = %v, want 2 at previous business day's rate 10", out["points"])
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var day string
	var delta int32
	err := tx.QueryRow(context.Background(), "SELECT business_date::text,delta FROM card_points_ledger WHERE card_id=$1", out["id"]).Scan(&day, &delta)
	dbtest.Rollback(t, tx)
	if err != nil || day != earlier.Format(time.DateOnly) || delta != 2 {
		t.Fatal(day, delta, err)
	}
}

func TestValidityDaysMaximum(t *testing.T) {
	s := open(t)
	in := typeInput("百年卡")
	in["validity_kind"] = "days_after_open"
	in["validity_days"] = 36501
	r := s.call("create_card_type", in, 422)
	if r["detail"] != "validation failed" {
		t.Fatal(r)
	}
	foundMaximum := false
	for _, raw := range r["errors"].([]any) {
		err := raw.(map[string]any)
		if err["location"] == "body.validity_days" && err["message"] == "expected number <= 36500" {
			foundMaximum = true
			break
		}
	}
	if !foundMaximum {
		t.Fatal(r)
	}
	in["validity_days"] = 36500
	if r := s.call("create_card_type", in, 200); r["validity_days"] != float64(36500) {
		t.Fatal(r)
	}
}
