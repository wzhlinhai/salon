package money

import "testing"

func TestTotalLimit(t *testing.T) {
	for _, tc := range []struct{ total, limit, want string }{{"200000000.00", "99999999.99", "99999999.99"}, {"30.25", "100.00", "30.25"}, {"0", "100", "0.00"}, {"100", "100", "100.00"}} {
		var total Total
		var limit Amount
		if err := total.Scan(tc.total); err != nil {
			t.Fatal(err)
		}
		if err := limit.Scan(tc.limit); err != nil {
			t.Fatal(err)
		}
		if got := total.Limit(limit); got.String() != tc.want {
			t.Fatal(got, tc)
		}
	}
}
