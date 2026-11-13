package orders_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) date(offset int) string {
	return settings.Date(s.Now()).AddDate(0, 0, offset).Format(settings.DateLayout)
}

func (s *fixture) paperOrder(date string, line orders.OrderItemInput) map[string]any {
	s.t.Helper()
	in := s.opening(line)
	in.CustomerID = &s.customer
	in.PaperDate = &date
	return s.call("open_order", in, 200)
}

func TestOpenOrderBackfillUsesPaperDayRules(t *testing.T) {
	s := open(t)
	today, yesterday := s.date(0), s.date(-1)
	s.call("change_setting", map[string]any{"key": settings.KeyMemberPriceRule, "value": "lowest", "effective_from": today}, 200)
	o := s.paperOrder(yesterday, s.line(1))
	if o["business_date"] != yesterday || o["backfill"] != true {
		t.Fatal(o)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var snapshotID uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT settings_snapshot_id FROM orders WHERE id=$1", id(o["id"])).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	rules, err := settings.LoadSnapshot(context.Background(), dbgen.New(tx), s.Res.StoreID, snapshotID)
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	if rules.MemberPriceRule() != "member_first" {
		t.Fatalf("补录单应使用纸单当天的规则，got %s", rules.MemberPriceRule())
	}
	normal := s.customerOrder(s.line(1))
	if normal["business_date"] != today || normal["backfill"] != false {
		t.Fatal(normal)
	}
}

func TestOpenOrderBackfillRejections(t *testing.T) {
	s := open(t)
	before := s.snapshot()
	for _, tc := range []struct {
		date   string
		detail string
	}{
		{s.date(1), "补录日期不能晚于今天"},
		{s.date(-4), "只能补录最近 3 个营业日的单"},
	} {
		in := s.opening(s.line(1))
		in.CustomerID = &s.customer
		in.PaperDate = &tc.date
		r := s.HTTPWithToken(t, s.Owner, "open_order", in)
		if r.Status != 409 || r.Body["detail"] != tc.detail || before != s.snapshot() {
			t.Fatal(r, tc.detail)
		}
	}
	s.call("change_setting", map[string]any{"key": settings.KeyBackfillMaxDays, "value": 0, "effective_from": s.date(0)}, 200)
	in := s.opening(s.line(1))
	in.CustomerID = &s.customer
	yesterday := s.date(-1)
	in.PaperDate = &yesterday
	if r := s.HTTPWithToken(t, s.Owner, "open_order", in); r.Status != 409 || r.Body["detail"] != "只能补录最近 0 个营业日的单" {
		t.Fatal(r)
	}
	today := s.date(0)
	in.PaperDate = &today
	if o := s.call("open_order", in, 200); o["business_date"] != today || o["backfill"] != true {
		t.Fatal(o)
	}
}

func TestOpenOrderBackfillRefusesSealedDay(t *testing.T) {
	s := open(t)
	s.call("seal_day", map[string]any{"date": s.date(-1)}, 200)
	in := s.opening(s.line(1))
	in.CustomerID = &s.customer
	yesterday := s.date(-1)
	in.PaperDate = &yesterday
	r := s.HTTPWithToken(t, s.Owner, "open_order", in)
	if r.Status != 409 || r.Body["detail"] != "该营业日已封账（"+yesterday+"）" {
		t.Fatal(r)
	}
}

func TestCheckoutBackfillUsesPaperDay(t *testing.T) {
	s := open(t)
	yesterday, today := s.date(-1), s.date(0)
	line := s.line(1, 2, 3)
	o := s.paperOrder(yesterday, line)
	now := s.Now()
	s.SetNow(now.AddDate(0, 0, -2))
	card := s.paymentCard(s.paymentType("补录卡", nil), "1000", "0")
	s.SetNow(now)
	after := lines(s.call("checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100"), cardPayment(card, "50"))), 200))[0].(map[string]any)
	if after["checkout_id"] == nil {
		t.Fatal(after)
	}
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var checkoutDay, ledgerDay, performanceDay, visitDay, collectionDay string
	if err := tx.QueryRow(ctx, "SELECT business_date::text FROM checkouts WHERE id=$1", id(after["checkout_id"])).Scan(&checkoutDay); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT business_date::text FROM card_ledger WHERE card_id=$1 ORDER BY at DESC, id DESC LIMIT 1", id(card["id"])).Scan(&ledgerDay); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT business_date::text FROM performance_entries WHERE order_id=$1 LIMIT 1", id(o["id"])).Scan(&performanceDay); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT business_date::text FROM guest_visits WHERE order_id=$1 LIMIT 1", id(o["id"])).Scan(&visitDay); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT business_date::text FROM collections WHERE order_id=$1 AND channel='cash'", id(o["id"])).Scan(&collectionDay); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	for label, day := range map[string]string{"checkout": checkoutDay, "ledger": ledgerDay, "performance": performanceDay, "visit": visitDay, "collection": collectionDay} {
		if day != yesterday {
			t.Fatalf("%s business date = %s, want %s", label, day, yesterday)
		}
	}
	yesterdayFigures := s.call("preview_day_close", map[string]any{"date": yesterday}, 200)
	todayFigures := s.call("preview_day_close", map[string]any{"date": today}, 200)
	if yesterdayFigures["checkout_count"] != float64(1) || todayFigures["checkout_count"] != float64(0) {
		t.Fatal(yesterdayFigures, todayFigures)
	}
	if onSite(yesterdayFigures, "cash") != "100.00" || onSite(todayFigures, "cash") != "0.00" {
		t.Fatal(yesterdayFigures, todayFigures)
	}
	if nonSite(yesterdayFigures, "card_principal") != "50.00" {
		t.Fatal(yesterdayFigures)
	}
}

func onSite(figures map[string]any, method string) string {
	for _, raw := range figures["on_site"].([]any) {
		row := raw.(map[string]any)
		if row["method"] == method {
			return row["amount"].(string)
		}
	}
	return ""
}

func nonSite(figures map[string]any, method string) string {
	for _, raw := range figures["non_site"].([]any) {
		row := raw.(map[string]any)
		if row["method"] == method {
			return row["amount"].(string)
		}
	}
	return ""
}

func TestCheckoutBackfillRefusedAfterSealing(t *testing.T) {
	s := open(t)
	o := s.paperOrder(s.date(-1), s.line(1))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO store_sealings (tenant_id,store_id,sealed_through) VALUES ($1,$2,$3) ON CONFLICT (tenant_id,store_id) DO UPDATE SET sealed_through=EXCLUDED.sealed_through`, s.Res.TenantID, s.Res.StoreID, s.date(-1))
	dbtest.Commit(t, tx)
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100"))))
	if r.Status != 409 || r.Body["detail"] != "该营业日已封账（"+s.date(-1)+"）" {
		t.Fatal(r)
	}
}

func TestCheckoutBackfillRejectsMixedDays(t *testing.T) {
	s := open(t)
	backfill := s.paperOrder(s.date(-1), s.line(1))
	normal := s.customerOrder(s.line(1))
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(backfill, payment("cash", "100")), checkoutOrder(normal, payment("cash", "100"))))
	if r.Status != 409 || r.Body["detail"] != "一次结账只能是同一个营业日" {
		t.Fatal(r)
	}
	older := s.paperOrder(s.date(-2), s.line(1))
	r = s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(backfill, payment("cash", "100")), checkoutOrder(older, payment("cash", "100"))))
	if r.Status != 409 || r.Body["detail"] != "一次结账只能是同一个营业日" {
		t.Fatal(r)
	}
}

func TestOpenOrderBackfillAICreatesDraft(t *testing.T) {
	s := open(t)
	before := s.snapshot()
	in := s.opening(s.line(1))
	in.CustomerID = &s.customer
	yesterday := s.date(-1)
	in.PaperDate = &yesterday
	r := s.AI(t, s.MustCaller(s.Owner), "open_order", in)
	optest.RequireDraftCreated(t, r, before == s.snapshot())
}

func TestBackfillMigrationDown(t *testing.T) {
	s := open(t)
	dbtest.MigrateDownTo(t, s.Pool, 49)
	var columns int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE (table_name='orders' AND column_name='backfill') OR (table_name='collections' AND column_name='business_date')").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("backfill columns remain after down: %d", columns)
	}
}

func (s *fixture) allowPayrollMonth(paper time.Time) {
	s.t.Helper()
	month := time.Date(paper.Year(), paper.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Pool.Exec(context.Background(), "UPDATE stores SET created_at=$1 WHERE id=$2", month.AddDate(0, -1, 0), s.Res.StoreID); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fixture) lockPayrollMonth(paper time.Time) {
	s.t.Helper()
	s.allowPayrollMonth(paper)
	month := time.Date(paper.Year(), paper.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Pool.Exec(context.Background(), "INSERT INTO commission_settlements(tenant_id,store_id,month,settled_by) VALUES($1,$2,$3,$4)", s.Res.TenantID, s.Res.StoreID, month, s.Res.OwnerID); err != nil {
		s.t.Fatal(err)
	}
}

func TestBackfillPayrollMonthLocked(t *testing.T) {
	s := open(t)
	paper := settings.Date(s.Now()).AddDate(0, 0, -1)
	paper = time.Date(paper.Year(), paper.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	paperText := paper.Format(settings.DateLayout)
	s.call("change_setting", map[string]any{"key": settings.KeyBackfillMaxDays, "value": 31, "effective_from": s.date(0)}, 200)
	s.lockPayrollMonth(paper)
	in := s.opening(s.line(1))
	in.CustomerID = &s.customer
	in.PaperDate = &paperText
	r := s.HTTPWithToken(t, s.Owner, "open_order", in)
	detail := paper.Format("2006-01") + " 已经锁定，不能再补录"
	if r.Status != 409 || r.Body["detail"] != detail {
		t.Fatal(r)
	}
}

func TestCheckoutBackfillPayrollMonthLockedAfterOpen(t *testing.T) {
	s := open(t)
	paper := settings.Date(s.Now()).AddDate(0, 0, -1)
	paper = time.Date(paper.Year(), paper.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	paperText := paper.Format(settings.DateLayout)
	s.call("change_setting", map[string]any{"key": settings.KeyBackfillMaxDays, "value": 31, "effective_from": s.date(0)}, 200)
	s.allowPayrollMonth(paper)
	o := s.paperOrder(paperText, s.line(1, 2, 3))
	s.lockPayrollMonth(paper)
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "150"))))
	detail := paper.Format("2006-01") + " 已经锁定，不能再补录"
	if r.Status != 409 || r.Body["detail"] != detail {
		t.Fatal(r)
	}
}

func TestMergeBackfillFlagMismatch(t *testing.T) {
	s := open(t)
	paper := s.date(-1)
	at, err := time.ParseInLocation(settings.DateLayout+" 15:04", paper+" 12:00", settings.Location)
	if err != nil {
		t.Fatal(err)
	}
	now := s.Now()
	s.SetNow(at)
	normal := s.customerOrder(s.line(1))
	s.SetNow(now)
	backfill := s.paperOrder(paper, s.line(1))
	s.assertMergeRefused(s.MustCaller(s.Owner), mergeInput(normal, backfill), 409, "补录单不能和普通单合并")
	s.assertMergeRefused(s.MustCaller(s.Owner), mergeInput(backfill, normal), 409, "补录单不能和普通单合并")
}
