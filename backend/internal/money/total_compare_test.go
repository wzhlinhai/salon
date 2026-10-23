package money_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestTotalCmpTotal(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0", "0.00", 0}, {"100.00", "100", 0}, {"0.10", "0.20", -1},
		{"199999999.98", "199999999.97", 1}, {"199999999.97", "199999999.98", -1},
	} {
		var a, b money.Total
		if err := a.Scan(tc.a); err != nil {
			t.Fatal(err)
		}
		if err := b.Scan(tc.b); err != nil {
			t.Fatal(err)
		}
		if got := a.CmpTotal(b); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
