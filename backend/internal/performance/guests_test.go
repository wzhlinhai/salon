package performance

import (
	"github.com/google/uuid"
	"reflect"
	"testing"
)

func TestGuestVisitAllocation(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	lines := []Line{{}, {Slots: []Slot{{Slot: 3, StaffID: &b}, {Slot: 1}, {Slot: 2, StaffID: &a}}}, {Slots: []Slot{{Slot: 1, StaffID: &a}, {Slot: 2, StaffID: &c}}}}
	for _, tc := range []struct {
		name, split string
		lines       []Line
		want        []guestShare
	}{
		{"first_slot", "first_slot", lines, []guestShare{{&a, 10000}}},
		{"equal_distinct_remainder", "equal", lines, []guestShare{{&a, 3334}, {&b, 3333}, {&c, 3333}}},
		{"no_lines", "first_slot", nil, []guestShare{{nil, 10000}}},
		{"empty_slots", "equal", []Line{{Slots: []Slot{{Slot: 1}}}}, []guestShare{{nil, 10000}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitGuestVisit(tc.lines, tc.split); !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, tc.want)
			}
		})
	}
}
