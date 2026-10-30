package performance

import (
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"testing"
)

func TestConsumeLabor(t *testing.T) {
	staff, second := uuid.New(), uuid.New()
	slots := []Slot{{Slot: 1, StaffID: &staff, SharePercent: new(int16(70))}, {Slot: 2, StaffID: &second, SharePercent: new(int16(30))}}
	percent := new(money.Percent)
	if err := percent.Scan("50"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		line          Line
		split         string
		kind, method  string
		paid, amounts []string
		store         bool
	}{
		{"paid split", Line{Count: &CountConsumption{Method: "count_paid", UnitPrice: amount("0.07")}, Slots: slots}, "by_position", "consume", "count_paid", []string{"0.05", "0.02"}, []string{"0.05", "0.02"}, false},
		{"gift zero ratio", Line{Count: &CountConsumption{Method: "count_gift", UnitPrice: amount("1")}}, "equal", "consume", "count_gift", []string{"1.00"}, []string{"0.00"}, true},
		{"paid store", Line{Count: &CountConsumption{Method: "count_paid", UnitPrice: amount("1")}}, "equal", "consume", "count_paid", []string{"1.00"}, []string{"1.00"}, true},
		{"fixed equal", Line{LaborFeeAmount: new(amount("0.07")), Slots: slots}, "equal", "labor", "labor", []string{"0.04", "0.03"}, []string{"0.04", "0.03"}, false},
		{"percent half fen", Line{Price: amount("0.01"), Net: amount("0"), LaborFeePercent: percent, Slots: slots[:1]}, "equal", "labor", "labor", []string{"0.01"}, []string{"0.01"}, false},
		{"labor no staff", Line{LaborFeeAmount: new(amount("1"))}, "equal", "", "", nil, nil, false},
		{"labor zero weight", Line{LaborFeeAmount: new(amount("1")), Slots: []Slot{{StaffID: &staff, SharePercent: new(int16(0))}}}, "by_position", "", "", nil, nil, false},
		{"zero fee", Line{LaborFeeAmount: new(amount("0")), Slots: slots}, "equal", "", "", nil, nil, false},
		{"zero unit", Line{Count: &CountConsumption{Method: "count_paid"}}, "equal", "", "", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := consumeAndLabor([]Line{tc.line}, tc.split, map[string]int{"count_paid": 100, "count_gift": 0})
			if len(got) != len(tc.paid) {
				t.Fatal(got)
			}
			for i, e := range got {
				if e.kind != tc.kind || e.method != tc.method || e.paidAmount.String() != tc.paid[i] || e.amount.String() != tc.amounts[i] || (e.staffID == nil) != tc.store {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestItemSkipsCount(t *testing.T) {
	countID, cashID := uuid.New(), uuid.New()
	got := allocate([]Line{{OrderItemID: countID, Net: amount("100"), Count: &CountConsumption{Method: "count_paid", UnitPrice: amount("25")}}, {OrderItemID: cashID, Net: amount("1")}}, []Category{{Method: "cash", Amount: amount("1")}}, "equal", map[string]int{"cash": 100})
	if len(got) != 1 || got[0].orderItemID != cashID || got[0].paidAmount.String() != "1.00" {
		t.Fatal(got)
	}
}
