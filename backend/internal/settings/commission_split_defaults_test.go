package settings

import (
	"encoding/json"
	"testing"
)

func TestOldSnapshotCommissionSplitDefaults(t *testing.T) {
	r := rulesFrom(map[string]json.RawMessage{"performance_split": json.RawMessage(`"full"`)})
	if r.CommissionSplit() != "same" {
		t.Fatal(r.CommissionSplit())
	}
}
