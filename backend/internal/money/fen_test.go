package money

import "testing"

func TestFen(t *testing.T) {
	for _, fen := range []int64{0, 1, 7, 12345, 9999999999} {
		a, err := FromFen(fen)
		if err != nil || a.Fen() != fen {
			t.Fatal(fen, a, err)
		}
	}
	for _, fen := range []int64{-1, 10000000000} {
		if _, err := FromFen(fen); err == nil {
			t.Fatal(fen)
		}
	}
}
