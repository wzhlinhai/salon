package money_test

import (
	"github.com/wzhlinhai/salon/backend/internal/money"
	"testing"
)

func TestPercentOf(t *testing.T) {
	for _, tc := range []struct {
		amount  string
		percent int
		want    string
	}{{"0.05", 10, "0.01"}, {"1.00", 0, "0.00"}, {"1.00", 100, "1.00"}, {"1.25", 10, "0.13"}} {
		var a money.Amount
		if err := a.Scan(tc.amount); err != nil {
			t.Fatal(err)
		}
		if got := a.PercentOf(tc.percent).String(); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}

func TestTotalDeduct(t *testing.T) {
	var a, b, d money.Amount
	for _, tc := range []struct{ a, b, d, want string }{
		{"99999999.99", "100", "200", "99999899.99"},
		{"10", "20", "30", "0.00"},
		{"99999999.99", "100", "0", "100000099.99"},
	} {
		for target, value := range map[*money.Amount]string{&a: tc.a, &b: tc.b, &d: tc.d} {
			if err := target.Scan(value); err != nil {
				t.Fatal(err)
			}
		}
		out := money.Total{}.AddProduct(a, 1).AddProduct(b, 1).Deduct(d)
		if out.Format() != tc.want {
			t.Fatal(out.Format(), tc.want)
		}
	}
	if err := a.Scan("0.05"); err != nil {
		t.Fatal(err)
	}
	got := money.Total{}.AddProduct(a, 1).PercentOf(10).Format()
	if got != "0.01" {
		t.Fatal(got)
	}
}

func TestTotalDeductExceedsTotal(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("negative total was hidden")
		}
	}()
	var deduction money.Amount
	if err := deduction.Scan("0.01"); err != nil {
		t.Fatal(err)
	}
	money.Total{}.Deduct(deduction)
}
