package booking

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func walkinAt(hour, minute int) time.Time {
	return time.Date(2026, 6, 12, hour, minute, 0, 0, time.UTC)
}

func walkinPtr[T any](v T) *T { return &v }

func walkinLineFor(orderID, customerID, staffID uuid.UUID, openedAt, addedAt time.Time, duration int) walkinLine {
	return walkinLine{
		OrderID: orderID, CustomerID: customerID, OpenedAt: openedAt,
		LineID: uuid.New(), AddedAt: addedAt, ItemKind: "service", DurationMinutes: duration, StaffID: &staffID,
	}
}

func TestWalkinIntervals(t *testing.T) {
	order, customer, staff, otherStaff := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t.Run("cursor starts at opened_at", func(t *testing.T) {
		got := walkinIntervals([]walkinLine{walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 60)}, 30)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(15, 0)}})
	})
	t.Run("line added after a gap starts at added_at", func(t *testing.T) {
		first := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 30)
		second := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(15, 0), 45)
		got := walkinIntervals([]walkinLine{first, second}, 30)
		assertWalkin(t, got, []WalkinOccupancy{
			{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(14, 30)},
			{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(15, 0), EndsAt: walkinAt(15, 45)},
		})
	})
	t.Run("removed line adds nothing", func(t *testing.T) {
		removed := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 120)
		removed.RemovedAt = walkinPtr(walkinAt(14, 10))
		active := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 30), 30)
		got := walkinIntervals([]walkinLine{removed, active}, 30)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 30), EndsAt: walkinAt(15, 0)}})
	})
	t.Run("goods line adds nothing", func(t *testing.T) {
		goods := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 120)
		goods.ItemKind = "goods"
		active := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 30), 30)
		got := walkinIntervals([]walkinLine{goods, active}, 30)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 30), EndsAt: walkinAt(15, 0)}})
	})
	t.Run("zero-duration group uses default once", func(t *testing.T) {
		zero := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 10), 0)
		got := walkinIntervals([]walkinLine{zero}, 45)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 10), EndsAt: walkinAt(14, 55)}})
	})
	t.Run("contiguous same-staff lines merge", func(t *testing.T) {
		first := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 60)
		second := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 30), 30)
		got := walkinIntervals([]walkinLine{first, second}, 30)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(15, 30)}})
	})
	t.Run("two zero-duration lines use one default interval", func(t *testing.T) {
		first := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 0)
		second := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 10), 0)
		got := walkinIntervals([]walkinLine{first, second}, 45)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(14, 45)}})
	})
	t.Run("zero duration inside a positive group adds nothing", func(t *testing.T) {
		zero := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 0)
		line := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 30)
		got := walkinIntervals([]walkinLine{zero, line}, 45)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(14, 30)}})
	})
	t.Run("two staff in one order run separate cursors", func(t *testing.T) {
		first := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(14, 0), 30)
		second := walkinLineFor(order, customer, otherStaff, walkinAt(14, 0), walkinAt(14, 0), 60)
		got := walkinIntervals([]walkinLine{first, second}, 30)
		if len(got) != 2 {
			t.Fatal(got)
		}
		for _, occ := range got {
			if occ.StaffID == staff && (occ.StartsAt != walkinAt(14, 0) || occ.EndsAt != walkinAt(14, 30)) {
				t.Fatal(occ)
			}
			if occ.StaffID == otherStaff && (occ.StartsAt != walkinAt(14, 0) || occ.EndsAt != walkinAt(15, 0)) {
				t.Fatal(occ)
			}
		}
	})
	t.Run("merged order uses target opened_at", func(t *testing.T) {
		line := walkinLineFor(order, customer, staff, walkinAt(14, 0), walkinAt(13, 50), 30)
		got := walkinIntervals([]walkinLine{line}, 30)
		assertWalkin(t, got, []WalkinOccupancy{{OrderID: order, CustomerID: customer, StaffID: staff, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(14, 30)}})
	})
}

func assertWalkin(t *testing.T, got, want []WalkinOccupancy) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("interval %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFilterWalkinOccupancies(t *testing.T) {
	self, other := uuid.New(), uuid.New()
	from, to := walkinAt(0, 0), walkinAt(23, 59)
	rows := []WalkinOccupancy{
		{OrderID: uuid.New(), StaffID: self, StartsAt: walkinAt(14, 0), EndsAt: walkinAt(15, 0)},
		{OrderID: uuid.New(), StaffID: other, StartsAt: walkinAt(15, 0), EndsAt: walkinAt(16, 0)},
	}
	for _, tc := range []struct {
		name    string
		grant   ops.Grant
		staffID *uuid.UUID
		want    int
	}{
		{"store all", ops.Grant{}, nil, 2},
		{"store filter", ops.Grant{}, &self, 1},
		{"self sees own", ops.Grant{Scope: ops.ScopeSelf}, nil, 1},
		{"self ignores other filter", ops.Grant{Scope: ops.ScopeSelf}, &other, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := ops.Env{Caller: ops.Caller{StaffID: self}, Grant: tc.grant}
			if got := filterWalkinOccupancies(env, rows, tc.staffID, from, to); len(got) != tc.want {
				t.Fatalf("got %+v, want %d", got, tc.want)
			}
		})
	}
}
