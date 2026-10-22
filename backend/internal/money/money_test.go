package money_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestJSON(t *testing.T) {
	for _, value := range []string{`"99999999.99"`, `"0.50"`, `"1234.56"`, `"0"`, `"1.2"`} {
		var a money.Amount
		if err := json.Unmarshal([]byte(value), &a); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		var b money.Amount
		if err := json.Unmarshal(data, &b); err != nil || a.String() != b.String() {
			t.Fatalf("%s: %s %v", value, data, err)
		}
	}
	for _, value := range []string{`"100000000.00"`, `"1.234"`, `"-1.00"`, `1.5`, `null`, `true`, `{}`, `"NaN"`, `"1e2"`} {
		var a money.Amount
		if err := json.Unmarshal([]byte(value), &a); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	for _, value := range []string{`"0.50"`, `"100.00"`, `"99.99"`} {
		var p money.Percent
		if err := json.Unmarshal([]byte(value), &p); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(p)
		if err != nil || string(data) != value {
			t.Fatalf("%s %v", data, err)
		}
	}
	for _, value := range []string{`"100.01"`, `"101"`, `"-1.00"`, `"1.234"`, `1.5`, `null`} {
		var p money.Percent
		if err := json.Unmarshal([]byte(value), &p); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestNumericRoundTrip(t *testing.T) {
	pool := dbtest.NewPool(t)
	tenant, _ := dbtest.NewTenant(t, pool, "金额测试")
	tx := dbtest.AppTx(t, pool, tenant)
	var a money.Amount
	var p money.Percent
	if err := json.Unmarshal([]byte(`"1234.56"`), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`"0.50"`), &p); err != nil {
		t.Fatal(err)
	}
	var got money.Amount
	var percent money.Percent
	if err := tx.QueryRow(context.Background(), "SELECT $1::numeric(10,2), $2::numeric(5,2)", a, p).Scan(&got, &percent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.String() != "1234.56" || percent.String() != "0.50" {
		t.Fatalf("%s %s", got, percent)
	}
}

func TestChangeJSON(t *testing.T) {
	for _, value := range []string{`"-0.50"`, `"-99999999.99"`, `"99999999.99"`, `"0.00"`} {
		var c money.Change
		if err := json.Unmarshal([]byte(value), &c); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(c)
		if err != nil || string(out) != value {
			t.Fatal(string(out), err)
		}
	}
	for _, value := range []string{`"-100000000.00"`, `"100000000.00"`, `"-0.501"`, `"+1.00"`, `1`, `null`, `"1e2"`} {
		var c money.Change
		if err := json.Unmarshal([]byte(value), &c); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestChangeNumericRoundTrip(t *testing.T) {
	pool := dbtest.NewPool(t)
	tenant, _ := dbtest.NewTenant(t, pool, "变动")
	tx := dbtest.AppTx(t, pool, tenant)
	for _, value := range []string{"-0.50", "-99999999.99", "99999999.99", "0.00"} {
		var c, got money.Change
		if err := c.Scan(value); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(context.Background(), "SELECT $1::numeric(10,2)", c).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got.String() != value {
			t.Fatal(got.String(), value)
		}
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAmountCmp(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0", "0.00", 0}, {"1.2", "1.20", 0}, {"9.99", "10.00", -1}, {"10.00", "9.99", 1},
		{"0.00", "0.01", -1}, {"0.01", "0.00", 1}, {"99999999.98", "99999999.99", -1},
	} {
		var a, b money.Amount
		if err := a.Scan(tc.a); err != nil {
			t.Fatal(err)
		}
		if err := b.Scan(tc.b); err != nil {
			t.Fatal(err)
		}
		if got := a.Cmp(b); got != tc.want {
			t.Fatalf("%s Cmp %s = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := b.Cmp(a); got != -tc.want {
			t.Fatalf("reverse Cmp = %d, want %d", got, -tc.want)
		}
	}
	var zero money.Amount
	var normalized money.Amount
	if err := normalized.Scan("0.00"); err != nil {
		t.Fatal(err)
	}
	if zero.Cmp(normalized) != 0 {
		t.Fatal("zero value differs from scanned zero")
	}
}
func TestTotalCmp(t *testing.T) {
	for _, tc := range []struct {
		total     any
		threshold string
		want      int
	}{
		{"0.00", "0.00", 0}, {"9.99", "10.00", -1}, {[]byte("10.00"), "10.00", 0},
		{"10.01", "10.00", 1}, {"100000000.00", "99999999.99", 1},
		{"123456789012345678901234567890.12", "99999999.99", 1},
	} {
		var total money.Total
		var threshold money.Amount
		if err := total.Scan(tc.total); err != nil {
			t.Fatal(err)
		}
		if err := threshold.Scan(tc.threshold); err != nil {
			t.Fatal(err)
		}
		if got := total.Cmp(threshold); got != tc.want {
			t.Fatalf("%v Cmp %s = %d, want %d", tc.total, tc.threshold, got, tc.want)
		}
	}
	var zero money.Total
	if zero.Cmp(money.Amount{}) != 0 {
		t.Fatal("zero cumulative amount differs from zero")
	}
	for _, value := range []any{"-1.00", "1.234", "NaN", "1e2", nil, 1.5} {
		var total money.Total
		if err := total.Scan(value); err == nil {
			t.Fatalf("accepted invalid total %v", value)
		}
	}
}
func TestTotalNumericScan(t *testing.T) {
	pool := dbtest.NewPool(t)
	tenant, _ := dbtest.NewTenant(t, pool, "累计金额")
	tx := dbtest.AppTx(t, pool, tenant)
	var total money.Total
	err := tx.QueryRow(context.Background(), "SELECT SUM(value) FROM (VALUES (99999999.99::numeric), (0.01::numeric)) AS amounts(value)").Scan(&total)
	if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	var threshold money.Amount
	if err := threshold.Scan("99999999.99"); err != nil {
		t.Fatal(err)
	}
	if total.Cmp(threshold) != 1 {
		t.Fatal("numeric sum lost precision or applied the single-amount limit")
	}
}

func TestAmountWholeDiv(t *testing.T) {
	for _, tc := range []struct {
		amount        string
		divisor, want int64
	}{
		{"0.00", 1, 0}, {"0.99", 1, 0}, {"1.00", 1, 1}, {"19.99", 10, 1}, {"20.00", 10, 2},
		{"20.01", 10, 2}, {"99999999.99", 1, 99999999}, {"99999999.99", 100000, 999},
		{"99999999.99", 9223372036854775807, 0},
	} {
		var amount money.Amount
		if err := amount.Scan(tc.amount); err != nil {
			t.Fatal(err)
		}
		if got := amount.WholeDiv(tc.divisor); got != tc.want {
			t.Fatalf("%s / %d = %d, want %d", tc.amount, tc.divisor, got, tc.want)
		}
	}
	if (money.Amount{}).WholeDiv(10) != 0 {
		t.Fatal("zero value division is nonzero")
	}
}

func TestAmountWholeDivRequiresPositiveDivisor(t *testing.T) {
	for _, divisor := range []int64{0, -1} {
		t.Run(fmt.Sprint(divisor), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("non-positive divisor accepted")
				}
			}()
			_ = (money.Amount{}).WholeDiv(divisor)
		})
	}
}
