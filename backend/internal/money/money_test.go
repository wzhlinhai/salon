package money_test

import (
	"context"
	"encoding/json"
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
