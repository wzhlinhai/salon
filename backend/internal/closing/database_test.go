package closing_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestClosingMigrationDown(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	f.Call(t, f.Owner, "seal_day", map[string]any{"date": "2026-10-01"}, 200)

	// 00047 Down must restore exactly the 00046 list: booking and groupbuy kinds
	// survive, while day_close/closing disappear.
	dbtest.MigrateDownTo(t, f.Pool, 46)
	for _, table := range []string{"sale_payments", "day_closes", "closing_events", "store_sealings"} {
		var name *string
		if err := f.Pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatal(table, name, err)
		}
	}
	var n int
	if err := f.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind IN ('day_close','closing')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	assertAuditKind(t, f, "day_close", true)
	assertAuditKind(t, f, "groupbuy_deal", false)
	assertAuditKind(t, f, "appointment_block", false)

	// 00046_booking Down removes the appointment kinds, keeping groupbuy.
	dbtest.MigrateDownTo(t, f.Pool, 45)
	assertAuditKind(t, f, "appointment_block", true)
	assertAuditKind(t, f, "groupbuy_deal", false)

	// 00045_groupbuy_order Down removes the groupbuy kinds too.
	dbtest.MigrateDownTo(t, f.Pool, 44)
	assertAuditKind(t, f, "groupbuy_deal", true)
}

func assertAuditKind(t *testing.T, f *fixture, kind string, wantRejected bool) {
	t.Helper()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test',$4,$5)`, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID, kind, uuid.New())
	var pe *pgconn.PgError
	if wantRejected {
		if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_target_kind_check" {
			t.Fatal(kind, err)
		}
	} else if err != nil {
		t.Fatal(kind, err)
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

func TestClosingForeignKeysAcrossTenants(t *testing.T) {
	f := open(t)
	other := otherTenant(t, f.Pool)
	var customer, cardType, card, ledger, packet uuid.UUID
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO customers(tenant_id,store_id,name,created_by) VALUES($1,$2,'别顾客',$3) RETURNING id`, other.TenantID, other.StoreID, other.OwnerID).Scan(&customer); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO card_types(tenant_id,store_id,name,kind,validity_kind) VALUES($1,$2,'别卡','stored_value','none') RETURNING id`, other.TenantID, other.StoreID).Scan(&cardType); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO cards(tenant_id,store_id,customer_id,card_type_id,opened_at,created_by) VALUES($1,$2,$3,$4,now(),$5) RETURNING id`, other.TenantID, other.StoreID, customer, cardType, other.OwnerID).Scan(&card); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO card_ledger(tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES($1,$2,$3,'recharge',0,1,1,0,0,0,current_date,$4) RETURNING id`, other.TenantID, other.StoreID, card, other.OwnerID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',10,'2030-01-01',current_date,$4) RETURNING id`, other.TenantID, other.StoreID, customer, other.OwnerID).Scan(&packet); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"sale card ledger", `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',1,current_date,$4)`, []any{f.Res.TenantID, f.Res.StoreID, ledger, f.Res.OwnerID}},
		{"sale red packet", `INSERT INTO sale_payments(tenant_id,store_id,red_packet_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',1,current_date,$4)`, []any{f.Res.TenantID, f.Res.StoreID, packet, f.Res.OwnerID}},
		{"sale creator", `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',1,current_date,$4)`, []any{f.Res.TenantID, f.Res.StoreID, ledger, other.OwnerID}},
		{"day close creator", `INSERT INTO day_closes(tenant_id,store_id,business_date,version,system,differences,cash_counted,unionpay_actual,wechat_actual,alipay_actual,created_by) VALUES($1,$2,current_date,1,'{}','{}',0,0,0,0,$3)`, []any{f.Res.TenantID, f.Res.StoreID, other.OwnerID}},
		{"closing event actor", `INSERT INTO closing_events(tenant_id,store_id,business_date,kind,actor_id) VALUES($1,$2,current_date,'seal',$3)`, []any{f.Res.TenantID, f.Res.StoreID, other.OwnerID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
			_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "23503" {
				t.Fatal(err)
			}
			dbtest.Rollback(t, tx)
		})
	}

	// RLS：另租户看不到本店日结和封账状态。
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	tx := dbtest.AppTx(t, f.Pool, other.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM day_closes WHERE store_id=$1", f.Res.StoreID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM store_sealings WHERE store_id=$1", f.Res.StoreID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	dbtest.Rollback(t, tx)
}

type queryCounter struct{ count atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestClosingQueryCounts(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)

	counter := &queryCounter{}
	config := f.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := f.MustCaller(f.Owner)
	counts := map[string]int64{}
	for _, n := range []int{1, 6} {
		if n > 1 {
			for i := 2; i <= n; i++ {
				day := at("2026-10-01", 12).AddDate(0, 0, i)
				f.SetNow(day)
				o := f.order(day.Format("2006-01-02"), f.customer, "10.00")
				f.checkout(o, payment("cash", "10"))
				f.SetNow(at("2026-10-02", 12).AddDate(0, 0, n))
				f.Call(t, f.Owner, "close_day", map[string]any{"date": day.Format("2006-01-02"), "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
			}
		}
		for _, tc := range []struct {
			name, op string
			args     map[string]any
		}{
			{"list", "list_day_closes", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-31"}},
			{"preview", "preview_day_close", map[string]any{"date": "2026-10-01"}},
		} {
			counter.count.Store(0)
			r := app.HTTP(t, &caller, tc.op, tc.args)
			if r.Status != 200 {
				t.Fatal(r)
			}
			got := counter.count.Load()
			if n == 1 {
				counts[tc.name] = got
			} else if got != counts[tc.name] {
				t.Fatalf("%s queries grew: %d -> %d", tc.name, counts[tc.name], got)
			}
		}
	}
	t.Run("store_sealings/delete", func(t *testing.T) {
		tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
		if _, err := tx.Exec(context.Background(), "DELETE FROM store_sealings"); err == nil {
			t.Fatal("store_sealings delete was allowed")
		} else if pe, ok := err.(*pgconn.PgError); !ok || pe.Code != "42501" {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
	})
}

func TestClosingDatabaseProtections(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.call("close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"})
	f.call("seal_day", map[string]any{"date": "2026-10-01"})
	f.openCard("1.00", "0.00", payment("cash", "1"))

	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var ledger uuid.UUID
	var day time.Time
	if err := tx.QueryRow(context.Background(), `SELECT id,business_date FROM card_ledger WHERE store_id=$1 ORDER BY id LIMIT 1`, f.Res.StoreID).Scan(&ledger, &day); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	var packet uuid.UUID
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',1,'2030-01-01',$4,$5) RETURNING id`, f.Res.TenantID, f.Res.StoreID, f.customer, day, f.Res.OwnerID).Scan(&packet); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"both sources", `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,red_packet_id,method,amount,business_date,created_by) VALUES($1,$2,$3,$4,'cash',1,$5,$6)`, []any{f.Res.TenantID, f.Res.StoreID, ledger, packet, day, f.Res.OwnerID}},
		{"no source", `INSERT INTO sale_payments(tenant_id,store_id,method,amount,business_date,created_by) VALUES($1,$2,'cash',1,$3,$4)`, []any{f.Res.TenantID, f.Res.StoreID, day, f.Res.OwnerID}},
		{"zero amount", `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',0,$4,$5)`, []any{f.Res.TenantID, f.Res.StoreID, ledger, day, f.Res.OwnerID}},
		{"bad method", `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'groupbuy',1,$4,$5)`, []any{f.Res.TenantID, f.Res.StoreID, ledger, day, f.Res.OwnerID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
			_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "23514" {
				t.Fatal(err)
			}
			dbtest.Rollback(t, tx)
		})
	}
	for _, table := range []string{"sale_payments", "day_closes", "closing_events"} {
		for _, verb := range []string{"UPDATE", "DELETE"} {
			t.Run(table+"/"+verb, func(t *testing.T) {
				sql := "UPDATE " + table + " SET tenant_id=tenant_id"
				if verb == "DELETE" {
					sql = "DELETE FROM " + table
				}
				tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
				_, err := tx.Exec(context.Background(), sql)
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "42501" {
					t.Fatal(err)
				}
				dbtest.Rollback(t, tx)
			})
		}
	}
	t.Run("store_sealings/delete", func(t *testing.T) {
		tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
		if _, err := tx.Exec(context.Background(), "DELETE FROM store_sealings"); err == nil {
			t.Fatal("store_sealings delete was allowed")
		} else if pe, ok := err.(*pgconn.PgError); !ok || pe.Code != "42501" {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
	})
}

func TestSalePaymentSourceStoreAndDate(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	card := f.openCard("1.00", "0.00", payment("cash", "1"))
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var ledger uuid.UUID
	var day time.Time
	if err := tx.QueryRow(context.Background(), `SELECT id,business_date FROM card_ledger WHERE card_id=$1 ORDER BY id LIMIT 1`, card["id"]).Scan(&ledger, &day); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)

	var otherStore, otherCustomer, otherType, otherCard, otherLedger uuid.UUID
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO stores(tenant_id,name) VALUES($1,'同租户别店') RETURNING id`, f.Res.TenantID).Scan(&otherStore); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO customers(tenant_id,store_id,name,created_by) VALUES($1,$2,'别店顾客',$3) RETURNING id`, f.Res.TenantID, otherStore, f.Res.OwnerID).Scan(&otherCustomer); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO card_types(tenant_id,store_id,name,kind,validity_kind) VALUES($1,$2,'别店卡','stored_value','none') RETURNING id`, f.Res.TenantID, otherStore).Scan(&otherType); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO cards(tenant_id,store_id,customer_id,card_type_id,opened_at,created_by) VALUES($1,$2,$3,$4,now(),$5) RETURNING id`, f.Res.TenantID, otherStore, otherCustomer, otherType, f.Res.OwnerID).Scan(&otherCard); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `INSERT INTO card_ledger(tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES($1,$2,$3,'recharge',0,1,1,0,0,0,$4,$5) RETURNING id`, f.Res.TenantID, otherStore, otherCard, day, f.Res.OwnerID).Scan(&otherLedger); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		store  uuid.UUID
		ledger uuid.UUID
		date   time.Time
	}{
		{"other store source", f.Res.StoreID, otherLedger, day},
		{"different business date", f.Res.StoreID, ledger, day.AddDate(0, 0, -1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
			_, err := tx.Exec(context.Background(), `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',1,$4,$5)`, f.Res.TenantID, tc.store, tc.ledger, tc.date, f.Res.OwnerID)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "23503" {
				t.Fatal(err)
			}
			dbtest.Rollback(t, tx)
		})
	}
}
