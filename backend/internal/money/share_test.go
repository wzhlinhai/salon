package money_test

import (
	"github.com/wzhlinhai/salon/backend/internal/money"
	"testing"
)

func TestShare(t *testing.T) {
	for _, tc := range []struct{ amount, percent, want string }{
		{"100", "12.50", "12.50"},
		{"60.04", "12.50", "7.51"},
		{"0.01", "50", "0.01"},
		{"0.01", "49.99", "0.00"},
		{"99999999.99", "100", "99999999.99"},
		{"100", "0", "0.00"},
	} {
		t.Run(tc.amount+"/"+tc.percent, func(t *testing.T) {
			var amount money.Amount
			var percent money.Percent
			if err := amount.Scan(tc.amount); err != nil {
				t.Fatal(err)
			}
			if err := percent.Scan(tc.percent); err != nil {
				t.Fatal(err)
			}
			if got := amount.Share(percent).String(); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
