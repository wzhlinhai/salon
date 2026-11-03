package orders_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestPerformanceEmptyRange(t *testing.T) { checkEmptyRange(t, "list_performance") }
func TestGuestVisitEmptyRange(t *testing.T)  { checkEmptyRange(t, "list_guest_visits") }
func TestCommissionEmptyRange(t *testing.T)  { checkEmptyRange(t, "list_commissions") }

func checkEmptyRange(t *testing.T, operation string) {
	t.Helper()
	s := open(t)
	s.commissionScheme("10")
	mine, token := s.Hire("本人", "098", "empty-range", s.Role("发型师"))
	absent, _ := s.Hire("无记录", "097", "absent-range")
	line := s.performanceLine(100)
	line.Staff[0].StaffID = mine
	s.paid(s.customerOrder(line), payment("cash", "100"))
	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
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
	owner, self := s.MustCaller(s.Owner), s.MustCaller(token)
	for _, ch := range app.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			args := commissionArgs(s.Now())
			normal := ch.Call(t, owner, operation, args)
			if normal.Status != 200 || len(normal.Body["entries"].([]any)) == 0 {
				t.Fatal(normal)
			}
			args["staff_id"] = absent
			counter.all.Store(0)
			real := ch.Call(t, owner, operation, args)
			realCount := counter.all.Load()
			args["staff_id"] = s.employee
			counter.all.Store(0)
			empty := ch.Call(t, self, operation, args)
			emptyCount := counter.all.Load()
			if real.Status != 200 || empty.Status != 200 {
				t.Fatal(real, empty)
			}
			if emptyCount >= realCount {
				t.Fatalf("Empty queries = %d, want fewer than SQL queries = %d", emptyCount, realCount)
			}
			realJSON, err := json.Marshal(real.Body)
			if err != nil {
				t.Fatal(err)
			}
			emptyJSON, err := json.Marshal(empty.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(realJSON, emptyJSON) {
				t.Fatalf("SQL=%s Empty=%s", realJSON, emptyJSON)
			}
		})
	}
}
