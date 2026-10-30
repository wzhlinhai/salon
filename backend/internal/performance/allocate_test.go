package performance

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func amount(v string) money.Amount {
	var a money.Amount
	if err := a.Scan(v); err != nil {
		panic(err)
	}
	return a
}

func TestAllocateMarginals(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	lines := []Line{{OrderItemID: a, Net: amount("0.03")}, {OrderItemID: b, Net: amount("0.04")}}
	got := allocate(lines, []Category{{Method: "wechat", Amount: amount("0.03")}, {Method: "cash", Amount: amount("0.04")}}, "equal", map[string]int{"cash": 100, "wechat": 100})
	// Floors are [1,1], [2,1]; northwest adds cash to row 1, wechat to row 2.
	want := []int64{2, 1, 2, 2}
	if len(got) != 4 {
		t.Fatal(got)
	}
	lineSums, methodSums := map[uuid.UUID]int64{}, map[string]int64{}
	for i, e := range got {
		if fen(e.paidAmount) != want[i] || fen(e.paidAmount) < 0 {
			t.Fatal(i, e)
		}
		lineSums[e.orderItemID] += fen(e.paidAmount)
		methodSums[e.method] += fen(e.paidAmount)
	}
	if lineSums[a] != 3 || lineSums[b] != 4 || methodSums["cash"] != 4 || methodSums["wechat"] != 3 {
		t.Fatal(lineSums, methodSums)
	}
}

func TestAllocateSlots(t *testing.T) {
	employee, position := uuid.New(), uuid.New()
	slot := func(n int16, share *int16, filled bool) Slot {
		s := Slot{Slot: n, PositionID: position, SharePercent: share}
		if filled {
			s.StaffID = &employee
		}
		return s
	}
	for _, tc := range []struct {
		name, split, cell string
		slots             []Slot
		want              []int64
		null              bool
	}{
		{"cash only", "equal", "1.00", nil, []int64{100}, true},
		{"50 30 20 remainder", "by_position", "0.07", []Slot{slot(1, new(int16(50)), true), slot(2, new(int16(30)), true), slot(3, new(int16(20)), true)}, []int64{4, 2, 1}, false},
		{"renormalise", "by_position", "0.07", []Slot{slot(1, new(int16(50)), false), slot(2, new(int16(30)), true), slot(3, new(int16(20)), true)}, []int64{5, 2}, false},
		{"equal ignores shares", "equal", "0.07", []Slot{slot(1, new(int16(90)), true), slot(2, new(int16(10)), true)}, []int64{4, 3}, false},
		{"old line no shares", "by_position", "0.07", []Slot{slot(1, nil, true), slot(2, nil, true)}, []int64{4, 3}, false},
		{"unfilled share missing", "by_position", "0.07", []Slot{slot(1, new(int16(90)), true), slot(2, new(int16(10)), true), slot(3, nil, false)}, []int64{4, 3}, false},
		{"no staff", "by_position", "1.00", []Slot{slot(1, new(int16(100)), false)}, []int64{100}, true},
		{"zero filled shares", "by_position", "1.00", []Slot{slot(1, new(int16(0)), true), slot(2, new(int16(100)), false)}, []int64{100}, true},
		{"same staff twice", "equal", "0.02", []Slot{slot(2, nil, true), slot(1, nil, true)}, []int64{1, 1}, false},
		{"zero slot omitted", "by_position", "0.01", []Slot{slot(1, new(int16(100)), true), slot(2, new(int16(0)), true)}, []int64{1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := allocate([]Line{{OrderItemID: uuid.New(), Net: amount(tc.cell), Slots: tc.slots}}, []Category{{Method: "cash", Amount: amount(tc.cell)}}, tc.split, map[string]int{"cash": 100})
			paid := []int64{}
			for _, e := range got {
				paid = append(paid, fen(e.paidAmount))
				if (e.staffID == nil) != tc.null || (e.slot == nil) != tc.null || (e.positionID == nil) != tc.null || e.amount.Cmp(e.paidAmount) != 0 {
					t.Fatal(e)
				}
			}
			if !reflect.DeepEqual(paid, tc.want) {
				t.Fatal(paid, tc.want)
			}
			if len(got) == 2 && !tc.null && *got[0].slot >= *got[1].slot {
				t.Fatal("slot order", got)
			}
		})
	}
}

func TestAllocateZeroAndRatio(t *testing.T) {
	for _, ratio := range []int{0, 50, 100} {
		got := allocate([]Line{{Net: amount("0.01")}}, []Category{{Method: "cash", Amount: amount("0.01")}, {Method: "wechat", Amount: amount("0")}}, "equal", map[string]int{"cash": ratio})
		want := "0.01"
		if ratio == 0 {
			want = "0.00"
		}
		if len(got) != 1 || got[0].amount.String() != want || got[0].paidAmount.String() != "0.01" {
			t.Fatal(got)
		}
	}
	if got := allocate([]Line{{Net: amount("0")}}, nil, "equal", nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestAllocateLargeAmounts(t *testing.T) {
	got := allocate([]Line{{Net: amount("99999999.99")}, {Net: amount("99999999.99")}}, []Category{{Method: "cash", Amount: amount("99999999.99")}, {Method: "alipay", Amount: amount("99999999.99")}}, "equal", map[string]int{"cash": 100, "alipay": 100})
	if len(got) != 4 || got[0].paidAmount.String() != "50000000.00" || got[1].paidAmount.String() != "49999999.99" {
		t.Fatal(got)
	}
}
