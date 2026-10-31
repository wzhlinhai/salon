package money_test

import (
	"github.com/wzhlinhai/salon/backend/internal/money"
	"testing"
)

func TestCommissionMoney(t *testing.T) {
	var price, paid money.Amount
	if err := price.Scan("123.45"); err != nil {
		t.Fatal(err)
	}
	if err := paid.Scan("70"); err != nil {
		t.Fatal(err)
	}
	var line money.Total
	if err := line.Scan("100"); err != nil {
		t.Fatal(err)
	}
	if got := price.Prorate(paid, line).String(); got != "86.42" {
		t.Fatal(got)
	}
	if got := price.Prorate(paid, money.Total{}).String(); got != "0.00" {
		t.Fatal(got)
	}
	change := price.Sub(money.Amount{})
	total := money.SignedTotal{}.Add(change).Add(change.Negate())
	if total.Format() != "0.00" {
		t.Fatal(total.Format())
	}
	total = total.Add(change.Negate())
	if total.Format() != "-123.45" {
		t.Fatal(total.Format())
	}
	var max money.Amount
	if err := max.Scan("99999999.99"); err != nil {
		t.Fatal(err)
	}
	total = money.SignedTotal{}.Add(max.Sub(money.Amount{})).Add(max.Sub(money.Amount{}))
	if total.Format() != "199999999.98" {
		t.Fatal(total.Format())
	}
}
