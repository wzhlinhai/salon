package collections

import (
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"testing"
)

func TestTransitions(t *testing.T) {
	states := []string{"pending", "processing", "succeeded", "failed", "cancelled", "refunded", "unknown"}
	legal := map[string]bool{"pending/processing": true, "pending/succeeded": true, "pending/failed": true, "pending/cancelled": true, "processing/succeeded": true, "processing/failed": true, "processing/cancelled": true, "succeeded/refunded": true, "succeeded/cancelled": true}
	for _, from := range states {
		for _, to := range states {
			t.Run(from+"/"+to, func(t *testing.T) {
				err := Transition(from, to)
				if legal[from+"/"+to] {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					want := ops.Rule("收款状态不能转换：" + from + " → " + to)
					if err == nil || ops.StatusOf(err) != 409 || err.Error() != want.Error() {
						t.Fatalf("got %v want %v", err, want)
					}
				}
			})
		}
	}
}
