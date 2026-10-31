package performance

import (
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"testing"
)

func TestSplitSale(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, tc := range []struct {
		value string
		n     int
		want  []string
	}{
		{"100", 3, []string{"33.34", "33.33", "33.33"}},
		{"0.01", 2, []string{"0.01"}},
		{"88", 1, []string{"88.00"}},
		{"0", 2, []string{}},
	} {
		t.Run(tc.value, func(t *testing.T) {
			var value money.Total
			if err := value.Scan(tc.value); err != nil {
				t.Fatal(err)
			}
			got := splitSale(value, ids[:tc.n])
			if len(got) != len(tc.want) {
				t.Fatal(got)
			}
			for i, s := range got {
				if s.staffID != ids[i] || s.amount.Format() != tc.want[i] {
					t.Fatal(got)
				}
			}
		})
	}
}
