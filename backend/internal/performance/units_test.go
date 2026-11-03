package performance

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func unitFixture() ([]dbgen.PerformanceEntry, map[lineKey]lineFact) {
	staff, item, line, checkout, position := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	entries := []dbgen.PerformanceEntry{}
	for _, slot := range []int16{1, 2} {
		for i, method := range []string{"cash", "card_principal", "card_bonus", "red_packet"} {
			paid := []string{"14", "21", "28", "7"}[i]
			value := []string{"14", "10.50", "7", "0.70"}[i]
			if slot == 2 {
				paid = []string{"6", "9", "12", "3"}[i]
				value = []string{"6", "4.50", "3", "0.30"}[i]
			}
			entries = append(entries, dbgen.PerformanceEntry{ID: uuid.New(), Kind: "item", BusinessDate: pgtype.Date{Time: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Valid: true}, CheckoutID: &checkout, OrderItemID: &line, StaffID: &staff, PositionID: &position, Slot: new(slot), Member: new(true), Method: method, PaidAmount: amount(paid), Amount: amount(value), Ratio: []int16{100, 50, 25, 10}[i], CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Valid: true}})
		}
	}
	return entries, map[lineKey]lineFact{{line, checkout}: {itemID: item, price: amount("123.45"), paid: "100.00", designated: true}}
}

func TestUnitComposition(t *testing.T) {
	entries, facts := unitFixture()
	units := composeUnits(entries, nil, facts)
	if len(units) != 2 {
		t.Fatal(units)
	}
	for _, u := range units {
		slot := int(*u.Slot) - 1
		for _, tc := range []struct {
			got  int64
			want string
		}{
			{u.Base.Paid, []string{"70.00", "30.00"}[slot]},
			{u.Base.Performance, []string{"32.20", "13.80"}[slot]},
			{u.Base.CashPerformance, []string{"14.00", "6.00"}[slot]},
			{u.Base.CardPerformance, []string{"17.50", "7.50"}[slot]},
			{u.Base.ProratedLinePrice, []string{"86.42", "37.04"}[slot]},
		} {
			got, err := money.FromFen(tc.got)
			if err != nil || got.String() != tc.want {
				t.Fatal(u, tc, err)
			}
		}
		if len(u.Entries) != 4 || u.Base.LinePrice.String() != "123.45" || u.Base.LinePaid != "100.00" || u.Base.LinePriceError != nil || !u.Designated || u.Member == nil || !*u.Member {
			t.Fatal(u)
		}
	}
	for _, method := range []string{"cash", "unionpay", "wechat", "alipay", "debt"} {
		t.Run(method, func(t *testing.T) {
			e, f := unitFixture()
			e = e[:1]
			e[0].Method = method
			out := composeUnits(e, nil, f)
			if len(out) != 1 || out[0].Base.CashPerformance != 1400 || out[0].Base.CardPerformance != 0 {
				t.Fatal(out)
			}
		})
	}
}

func TestUnitStoreSkip(t *testing.T) {
	e, f := unitFixture()
	for i := range e {
		e[i].StaffID = nil
	}
	if out := composeUnits(e, nil, f); len(out) != 0 {
		t.Fatal(out)
	}
}

func TestUnitIdentityAndReversal(t *testing.T) {
	e, f := unitFixture()
	e = e[:4]
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	e[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	e[1].CreatedAt.Time = e[0].CreatedAt.Time.Add(time.Minute)
	e[2].CreatedAt.Time = e[0].CreatedAt.Time.Add(-time.Minute)
	original := composeUnits(e, nil, f)
	if len(original) != 1 || original[0].ID != e[1].ID || original[0].CreatedAt != e[2].CreatedAt.Time || original[0].Entries[0].ID != e[1].ID {
		t.Fatal(original)
	}
	for i := 1; i < len(original[0].Entries); i++ {
		if original[0].Entries[i-1].ID.String() >= original[0].Entries[i].ID.String() {
			t.Fatal(original)
		}
	}
	reversals := slices.Clone(e)
	for i := range reversals {
		reversals[i].ID = uuid.New()
		reversals[i].ReversesID = &e[i].ID
		reversals[i].VoidID = new(uuid.MustParse("00000000-0000-0000-0000-000000000003"))
		reversals[i].BusinessDate.Time = e[i].BusinessDate.Time.AddDate(0, 0, 2)
	}
	reversed := composeUnits(reversals, e, f)
	if len(reversed) != 1 || reversed[0].Reverses == nil || reversed[0].Reverses.ID != original[0].ID || reversed[0].Reverses.Base.Performance != 3220 {
		t.Fatal(reversed)
	}
	both := composeUnits(append(slices.Clone(e), reversals...), e, f)
	if len(both) != 2 {
		t.Fatal(both)
	}
	for _, u := range both {
		if u.Base.Performance != 3220 || len(u.Entries) != 4 {
			t.Fatal(u)
		}
	}
}

func TestUnitGroupingKeys(t *testing.T) {
	for _, key := range []string{"kind", "checkout", "void", "line", "slot", "sale"} {
		t.Run(key, func(t *testing.T) {
			e, f := unitFixture()
			e = e[:2]
			switch key {
			case "kind":
				e[1].Kind = "labor"
			case "checkout":
				e[1].CheckoutID = new(uuid.New())
			case "void":
				e[1].VoidID = new(uuid.New())
			case "line":
				e[1].OrderItemID = new(uuid.New())
			case "slot":
				e[1].Slot = new(int16(2))
			case "sale":
				for i := range e {
					e[i].Kind = "sale"
					e[i].Member = nil
					e[i].CheckoutID = nil
					e[i].OrderItemID = nil
					e[i].Slot = nil
					e[i].PositionID = nil
				}
			}
			out := composeUnits(e, nil, f)
			if len(out) != 2 {
				t.Fatal(out)
			}
			if key == "sale" {
				for _, u := range out {
					if len(u.Entries) != 1 || u.Member != nil || u.Designated {
						t.Fatal(u)
					}
				}
			}
		})
	}
}

func TestUnitLinePriceErrors(t *testing.T) {
	e, f := unitFixture()
	e = e[:1]
	key := lineKey{*e[0].OrderItemID, *e[0].CheckoutID}
	fact := f[key]
	fact.paid = "bad"
	f[key] = fact
	out := composeUnits(e, nil, f)
	if out[0].Base.LinePriceError == nil || out[0].Base.Performance != 1400 {
		t.Fatal(out)
	}
	fact.paid = "100.00"
	f[key] = fact
	e[0].PaidAmount = amount("99999999.99")
	second := e[0]
	second.ID = uuid.New()
	out = composeUnits(append(e, second), nil, f)
	if out[0].Base.LinePriceError == nil || out[0].Base.Paid != 19999999998 {
		t.Fatal(out)
	}
}

func TestUnitDedup(t *testing.T) {
	e, f := unitFixture()
	out := composeUnits(append(slices.Clone(e), e...), e, f)
	if len(out) != 2 {
		t.Fatal(out)
	}
	for _, u := range out {
		if len(u.Entries) != 4 || u.Base.Performance != []int64{3220, 1380}[*u.Slot-1] {
			t.Fatal(u)
		}
	}
}
