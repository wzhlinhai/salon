package money_test

import (
	"fmt"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestTotalSplitRejectsNonpositiveParts(t *testing.T) {
	for _, parts := range []int{0, -1} {
		t.Run(fmt.Sprint(parts), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("nonpositive parts accepted")
				}
			}()
			money.Total{}.Split(parts)
		})
	}
}
