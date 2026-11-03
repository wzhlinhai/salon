package commission

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"testing"
)

func amt(s string) money.Amount {
	var a money.Amount
	if err := a.Scan(s); err != nil {
		panic(err)
	}
	return a
}
func pct(s string) *money.Percent {
	var p money.Percent
	if err := p.Scan(s); err != nil {
		panic(err)
	}
	return &p
}
func date(s string) pgtype.Date {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return pgtype.Date{Time: d, Valid: true}
}
func version(n int32, day string, at time.Time, s CommissionScheme) dbgen.CommissionSchemeVersion {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return dbgen.CommissionSchemeVersion{ID: uuid.New(), Version: n, EffectiveFrom: date(day), SavedAt: pgtype.Timestamptz{Time: at, Valid: true}, Scheme: raw}
}
func calcFixture() ([]performance.Unit, CommissionScheme, []dbgen.CommissionSchemeVersion) {
	staff, item, line, checkout, position := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	units := []performance.Unit{}
	for _, slot := range []int16{1, 2} {
		entries := []performance.UnitEntry{}
		for i, method := range []string{"cash", "card_principal", "card_bonus", "red_packet"} {
			paid := []string{"14", "21", "28", "7"}[i]
			value := []string{"14", "10.50", "7", "0.70"}[i]
			if slot == 2 {
				paid = []string{"6", "9", "12", "3"}[i]
				value = []string{"6", "4.50", "3", "0.30"}[i]
			}
			entries = append(entries, performance.UnitEntry{ID: uuid.UUID{14: byte(slot), 15: byte(i + 1)}, Method: method, PaidAmount: amt(paid), Amount: amt(value), Ratio: []int16{100, 50, 25, 10}[i]})
		}
		u := performance.Unit{ID: entries[0].ID, Kind: "item", BusinessDate: date("2026-10-31").Time, CheckoutID: &checkout, OrderItemID: &line, ItemID: item, StaffID: staff, PositionID: &position, Slot: new(slot), Member: new(true), Designated: true, CreatedAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Entries: entries}
		u.Base = performance.UnitBase{Paid: []int64{7000, 3000}[slot-1], Performance: []int64{3220, 1380}[slot-1], CashPerformance: []int64{1400, 600}[slot-1], CardPerformance: []int64{1750, 750}[slot-1], LinePrice: amt("123.45"), LinePaid: "100.00", ProratedLinePrice: []int64{8642, 3704}[slot-1]}
		units = append(units, u)
	}
	s := CommissionScheme{Rules: []Rule{{Source: "item", Base: "performance", Cells: []Cell{{Percent: pct("10")}}}}}
	return units, s, []dbgen.CommissionSchemeVersion{version(1, "2026-10-31", units[0].CreatedAt.Add(-time.Hour), s)}
}

func singleEntry(u performance.Unit) performance.Unit {
	u.Entries = slices.Clone(u.Entries[:1])
	u.Base = performance.UnitBase{Paid: 1400, Performance: 1400, CashPerformance: 1400, LinePrice: amt("123.45"), LinePaid: "100.00", ProratedLinePrice: 1728}
	return u
}

func runCalc(t *testing.T, e []performance.Unit, s CommissionScheme) []Commission {
	t.Helper()
	v := version(1, "2026-10-31", e[0].CreatedAt.Add(-time.Hour), s)
	out, err := calculate(e, []dbgen.CommissionSchemeVersion{v})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestCommissionBases(t *testing.T) {
	for _, tc := range []struct {
		base string
		want []string
	}{{"paid", []string{"70.00", "30.00"}}, {"performance", []string{"32.20", "13.80"}}, {"cash_performance", []string{"14.00", "6.00"}}, {"card_performance", []string{"17.50", "7.50"}}, {"line_price", []string{"86.42", "37.04"}}} {
		t.Run(tc.base, func(t *testing.T) {
			e, s, _ := calcFixture()
			s.Rules[0].Base = tc.base
			out := runCalc(t, e, s)
			if len(out) != 2 {
				t.Fatal(out)
			}
			for _, c := range out {
				slot := int(*c.Explanation.Origin.Slot) - 1
				if c.Explanation.Base.Amount.String() != tc.want[slot] || c.Amount.String() != amt(tc.want[slot]).Share(*pct("10")).String() || len(c.Explanation.Entries) != 4 {
					t.Fatal(c)
				}
			}
		})
	}
	for _, source := range []string{"consume", "labor", "sale"} {
		t.Run(source, func(t *testing.T) {
			e, s, _ := calcFixture()
			e = []performance.Unit{singleEntry(e[0])}
			e[0].Kind = source
			s.Rules[0].Source = source
			out := runCalc(t, e, s)
			if len(out) != 1 || out[0].Amount.String() != "1.40" {
				t.Fatal(out)
			}
		})
	}
}
func TestCommissionFixedUnit(t *testing.T) {
	e, s, _ := calcFixture()
	e = e[:1]
	e[0].Entries = e[0].Entries[:3]
	e[0].Base.Performance = 3150
	e[0].Base.Paid = 6300
	e[0].Base.ProratedLinePrice = 7777
	s.Rules[0].Cells = []Cell{{FixedAmount: new(amt("2.34"))}}
	out := runCalc(t, e, s)
	if len(out) != 1 || out[0].Amount.String() != "2.34" || len(out[0].Explanation.Entries) != 3 {
		t.Fatal(out)
	}
	for i := range e[0].Entries {
		e[0].Entries[i].Amount = amt("0")
	}
	e[0].Base.Performance = 0
	out = runCalc(t, e, s)
	if len(out) != 1 || out[0].Amount.String() != "2.34" {
		t.Fatal(out)
	}
	s.Rules[0].Cells = []Cell{{Percent: pct("50")}}
	out = runCalc(t, e, s)
	if len(out) != 1 || out[0].Amount.String() != "0.00" {
		t.Fatal(out)
	}
	e = []performance.Unit{singleEntry(e[0])}
	e[0].Entries[0].Amount = amt("0.01")
	e[0].Base.Performance = 1
	out = runCalc(t, e, s)
	if out[0].Amount.String() != "0.01" {
		t.Fatal(out)
	}
}
func TestCommissionMatchOrder(t *testing.T) {
	e, s, _ := calcFixture()
	e = []performance.Unit{singleEntry(e[0])}
	item := e[0].ItemID
	overrides := []Override{{Source: "item", StaffID: &e[0].StaffID, Percent: pct("20")}, {Source: "item", ItemID: &item, Percent: pct("30")}, {Source: "item", StaffID: &e[0].StaffID, ItemID: &item, Percent: pct("40")}}
	for _, tc := range []struct {
		n            int
		kind, amount string
	}{{3, "override_staff_item", "5.60"}, {2, "override_item", "4.20"}, {1, "override_staff", "2.80"}, {0, "cell", "1.40"}} {
		t.Run(tc.kind, func(t *testing.T) {
			s.Overrides = overrides[:tc.n]
			out := runCalc(t, e, s)
			if len(out) != 1 || out[0].Amount.String() != tc.amount || out[0].Explanation.Match.Kind != tc.kind {
				t.Fatal(out)
			}
		})
	}
	s.Rules[0].Cells = nil
	s.Overrides = overrides[:1]
	out := runCalc(t, e, s)
	if len(out) != 1 || out[0].Amount.String() != "2.80" {
		t.Fatal(out)
	}
	e[0].Kind = "sale"
	s.Rules[0].Source = "sale"
	s.Rules[0].Cells = []Cell{{Percent: pct("10")}}
	for i := range overrides {
		overrides[i].Source = "sale"
	}
	s.Overrides = overrides
	out = runCalc(t, e, s)
	if len(out) != 1 || out[0].Explanation.Match.Kind != "override_staff" || out[0].Amount.String() != "2.80" {
		t.Fatal(out)
	}
	s.Overrides = overrides[1:]
	out = runCalc(t, e, s)
	if len(out) != 1 || out[0].Explanation.Match.Kind != "cell" {
		t.Fatal(out)
	}
}
func TestCommissionCellKeysAndSkips(t *testing.T) {
	for _, key := range []string{"position", "designated", "member"} {
		t.Run(key, func(t *testing.T) {
			e, s, _ := calcFixture()
			e = []performance.Unit{singleEntry(e[0])}
			wrong, right := Cell{Percent: pct("80")}, Cell{Percent: pct("10")}
			switch key {
			case "position":
				s.Rules[0].ByPosition = true
				wrong.PositionID = new(uuid.New())
				right.PositionID = e[0].PositionID
			case "designated":
				s.Rules[0].ByDesignated = true
				wrong.Designated = new(false)
				right.Designated = new(true)
			case "member":
				s.Rules[0].ByMember = true
				wrong.Member = new(false)
				right.Member = new(true)
			}
			s.Rules[0].Cells = []Cell{wrong, right}
			out := runCalc(t, e, s)
			if len(out) != 1 || out[0].Amount.String() != "1.40" {
				t.Fatal(out)
			}
			s.Rules[0].Cells = []Cell{wrong}
			if out := runCalc(t, e, s); len(out) != 0 {
				t.Fatal(out)
			}
		})
	}
	for _, skip := range []string{"excluded", "version"} {
		t.Run(skip, func(t *testing.T) {
			e, s, v := calcFixture()
			switch skip {
			case "excluded":
				s.ExcludedStaff = []uuid.UUID{e[0].StaffID}
				v = []dbgen.CommissionSchemeVersion{version(1, "2026-10-31", e[0].CreatedAt.Add(-time.Hour), s)}
			case "version":
				v = nil
			}
			out, err := calculate(e, v)
			if err != nil || len(out) != 0 {
				t.Fatal(out, err)
			}
		})
	}
}
func TestCommissionUnitVersionsAndReversal(t *testing.T) {
	e, s, v := calcFixture()
	e = e[:1]
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	newer := s
	newer.Rules = []Rule{{Source: "item", Base: "performance", Cells: []Cell{{Percent: pct("90")}}}}
	v = append(v, version(2, "2026-10-31", e[0].CreatedAt.Add(time.Second), newer), version(3, "2026-11-01", e[0].CreatedAt.Add(-time.Second), newer))
	// 单位的最早写入时间决定版本。
	out, err := calculate(e, v)
	if err != nil || len(out) != 1 || out[0].Amount.String() != "3.22" || out[0].Explanation.Version.Version != 1 {
		t.Fatal(out, err)
	}
	original := out[0]
	void := uuid.New()
	reversals := append([]performance.Unit{}, e...)
	for i := range reversals {
		reversals[i].ID = uuid.New()
		reversals[i].Reverses = &e[i]
		reversals[i].VoidID = &void
		reversals[i].BusinessDate = date("2026-11-02").Time
		reversals[i].CreatedAt = e[i].CreatedAt.Add(48 * time.Hour)
	}
	out, err = calculate(reversals, v)
	if err != nil || len(out) != 1 || out[0].Amount.String() != "-3.22" || out[0].Explanation.Version.Version != 1 || *out[0].Explanation.Reverses != original.ID || out[0].Explanation.Match.Cell.Percent.String() != "10.00" || out[0].BusinessDate != "2026-11-02" {
		t.Fatal(out, err)
	}
	// 原笔与冲回同范围时，原记录不能被重复计入基数。
	both := append(append([]performance.Unit{}, e...), reversals...)
	out, err = calculate(both, v)
	if err != nil || len(out) != 2 || out[0].Amount.String() != "-3.22" || out[1].Amount.String() != "3.22" {
		t.Fatal(out, err)
	}
}
func TestSelectCommissionVersion(t *testing.T) {
	_, s, _ := calcFixture()
	day := date("2026-10-31").Time
	cut := day.Add(time.Hour)
	v := []dbgen.CommissionSchemeVersion{version(3, "2026-11-01", day, s), version(2, "2026-10-31", cut, s), version(1, "2026-10-31", day, s), version(4, "2026-10-30", day, s)}
	// 与真实批量加载一致按版本号升序，同时用逆序验证选择不依赖输入顺序。
	ascending := slices.Clone(v)
	slices.SortFunc(ascending, func(a, b dbgen.CommissionSchemeVersion) int { return cmp.Compare(a.Version, b.Version) })
	reversed := slices.Clone(ascending)
	slices.Reverse(reversed)
	for _, order := range []struct {
		name     string
		versions []dbgen.CommissionSchemeVersion
	}{
		{"original", v}, {"version_ascending", ascending}, {"version_reversed", reversed},
	} {
		t.Run(order.name, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				day  time.Time
				cut  *time.Time
				want int32
			}{
				{"equal_date_higher_version", day, &cut, 2},
				{"later_saved_version_excluded", day, new(cut.Add(-time.Nanosecond)), 1},
				{"later_effective_date_promoted", day.AddDate(0, 0, 1), nil, 3},
				{"future_versions_excluded", day.AddDate(0, 0, -2), nil, 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := selectVersion(order.versions, tc.day, tc.cut)
					if got.Version != tc.want || (err != nil) != (tc.want == 0) {
						t.Fatal(got, err, tc)
					}
				})
			}
		})
	}
}

func TestCommissionUnitOrderAndTargets(t *testing.T) {
	e, s, v := calcFixture()
	// 单位已由 performance 归并，列表排序仍用最早写入时刻。
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	e[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	e[0].CreatedAt = e[0].CreatedAt.Add(-time.Minute)
	out, err := calculate(e, v)
	if err != nil || len(out) != 2 || *out[0].Explanation.Origin.Slot != 2 || out[1].ID != e[0].ID {
		t.Fatal(out, err)
	}
	e = []performance.Unit{singleEntry(e[0])}
	for _, o := range []Override{{Source: "sale", StaffID: &e[0].StaffID, Percent: pct("90")}, {Source: "item", StaffID: new(uuid.New()), Percent: pct("90")}, {Source: "item", ItemID: new(uuid.New()), Percent: pct("90")}} {
		s.Overrides = []Override{o}
		out := runCalc(t, e, s)
		if len(out) != 1 || out[0].Amount.String() != "1.40" || out[0].Explanation.Match.Kind != "cell" {
			t.Fatal(out)
		}
	}
	// 一条销售业绩就是一笔；多来源不能混合，也不能跨结账合并。
	e, s, _ = calcFixture()
	sale := e[0]
	sale.Entries = slices.Clone(e[0].Entries[1:2])
	sale.ID = sale.Entries[0].ID
	sale.Base = performance.UnitBase{Paid: 2100, Performance: 1050, CardPerformance: 1050}
	e = []performance.Unit{singleEntry(e[0]), sale}
	e[1].Kind = "sale"
	e[1].Member = nil
	e[1].CheckoutID = nil
	e[1].OrderItemID = nil
	e[1].Slot = nil
	e[1].PositionID = nil
	s.Rules = append(s.Rules, Rule{Source: "sale", Base: "performance", Cells: []Cell{{FixedAmount: new(amt("2"))}}})
	out = runCalc(t, e, s)
	if len(out) != 2 {
		t.Fatal(out)
	}
	for _, c := range out {
		if c.Source == "sale" && (c.Amount.String() != "2.00" || c.Explanation.Member != nil || c.Explanation.Designated != nil) {
			t.Fatal(c)
		}
	}
}

func tierFixture(mode string, amounts ...string) ([]performance.Unit, CommissionScheme, []dbgen.CommissionSchemeVersion) {
	staff := uuid.New()
	entries := []performance.Unit{}
	for i, a := range amounts {
		id := uuid.New()
		entries = append(entries, performance.Unit{ID: id, Kind: "sale", StaffID: staff, BusinessDate: date("2026-10-15").Time, Base: performance.UnitBase{Performance: amt(a).Fen(), Paid: amt(a).Fen()}, Entries: []performance.UnitEntry{{ID: id, Amount: amt(a), PaidAmount: amt(a)}}, CreatedAt: date("2026-10-15").Time.Add(time.Duration(i) * time.Hour)})
	}
	s := CommissionScheme{Rules: []Rule{{Source: "sale", Base: "performance", Cells: []Cell{{Tiers: &Tiers{Mode: mode, Levels: []TierLevel{{From: amt("0"), Percent: *pct("10")}, {From: amt("10000"), Percent: *pct("15")}}}}}}}}
	return entries, s, []dbgen.CommissionSchemeVersion{version(1, "2026-09-01", date("2026-09-01").Time, s)}
}
func tierCalculate(t *testing.T, entries []performance.Unit, versions []dbgen.CommissionSchemeVersion) map[uuid.UUID]Commission {
	t.Helper()
	out, err := calculate(entries, versions)
	if err != nil {
		t.Fatal(err)
	}
	result := map[uuid.UUID]Commission{}
	for _, c := range out {
		result[c.ID] = c
		if tier := c.Explanation.Tier; tier != nil {
			var sum int64
			for _, p := range tier.Parts {
				sum += p.Amount.Fen()
			}
			if sum != c.Amount.Fen() {
				t.Fatal(c)
			}
		}
	}
	return result
}
func TestCommissionTiersWhole(t *testing.T) {
	e, _, v := tierFixture("whole", "6000", "5000")
	out := tierCalculate(t, e, v)
	if out[e[0].ID].Amount.String() != "900.00" || out[e[1].ID].Amount.String() != "750.00" {
		t.Fatal(out)
	}
	tier := out[e[0].ID].Explanation.Tier
	if tier.Mode != "whole" || tier.Month != "2026-10" || tier.BaseKind != "performance" || tier.Cumulative.String() != "11000.00" || tier.Before != nil || tier.After != nil || tier.Level.Index != 1 || tier.Level.From.String() != "10000.00" || tier.Level.Percent.String() != "15.00" || len(tier.Parts) != 1 || tier.Parts[0].Base.String() != "6000.00" || tier.Parts[0].Amount.String() != "900.00" {
		t.Fatal(tier)
	}
	out = tierCalculate(t, e[:1], v)
	if out[e[0].ID].Amount.String() != "600.00" {
		t.Fatal(out)
	}
	e[1].Base.Performance = amt("4000").Fen()
	e[1].Entries[0].Amount = amt("4000")
	out = tierCalculate(t, e, v)
	if out[e[0].ID].Amount.String() != "900.00" || out[e[0].ID].Explanation.Tier.Cumulative.String() != "10000.00" {
		t.Fatal(out)
	}
}
func TestCommissionTiersProgressive(t *testing.T) {
	e, _, v := tierFixture("progressive", "6000", "6000")
	out := tierCalculate(t, e, v)
	if out[e[0].ID].Amount.String() != "600.00" || out[e[1].ID].Amount.String() != "700.00" {
		t.Fatal(out)
	}
	tier := out[e[1].ID].Explanation.Tier
	if tier.Mode != "progressive" || tier.Month != "2026-10" || tier.BaseKind != "performance" || tier.Cumulative != nil || tier.Before.String() != "6000.00" || tier.After.String() != "12000.00" || tier.Level.Index != 1 || tier.Level.From.String() != "10000.00" || tier.Level.Percent.String() != "15.00" || len(tier.Parts) != 2 || tier.Parts[0].Base.String() != "4000.00" || tier.Parts[0].Amount.String() != "400.00" || tier.Parts[1].Base.String() != "2000.00" || tier.Parts[1].Amount.String() != "300.00" {
		t.Fatal(tier)
	}
	e[0].Base.Performance = amt("10000").Fen()
	e[0].Entries[0].Amount = amt("10000")
	out = tierCalculate(t, e, v)
	tier = out[e[1].ID].Explanation.Tier
	if len(tier.Parts) != 1 || tier.Parts[0].From.String() != "10000.00" || out[e[1].ID].Amount.String() != "900.00" {
		t.Fatal(out)
	}
}
func TestCommissionTiersGrouping(t *testing.T) {
	for _, mode := range []string{"whole", "progressive"} {
		for _, group := range []string{"month", "staff", "source"} {
			t.Run(mode+group, func(t *testing.T) {
				e, _, v := tierFixture(mode, "10000", "6000")
				switch group {
				case "month":
					e[0].BusinessDate = date("2026-09-30").Time
				case "staff":
					e[0].StaffID = uuid.New()
				case "source":
					e[0].Kind = "labor"
				}
				out := tierCalculate(t, e, v)
				if out[e[1].ID].Amount.String() != "600.00" {
					t.Fatal(out)
				}
			})
		}
	}
}
func TestCommissionTiersAllPerformance(t *testing.T) {
	for _, mode := range []string{"whole", "progressive"} {
		for _, match := range []string{"percent", "override", "unmatched", "excluded", "no-version"} {
			t.Run(mode+match, func(t *testing.T) {
				e, s, _ := tierFixture(mode, "10000", "6000")
				old := s
				switch match {
				case "percent":
					old.Rules = []Rule{{Source: "sale", Base: "performance", Cells: []Cell{{Percent: pct("1")}}}}
				case "override":
					old.Overrides = []Override{{Source: "sale", StaffID: &e[0].StaffID, Percent: pct("1")}}
				case "unmatched":
					old.Rules = nil
				case "excluded":
					old.ExcludedStaff = []uuid.UUID{e[0].StaffID}
				}
				v := []dbgen.CommissionSchemeVersion{version(1, "2026-09-01", date("2026-09-01").Time, old), version(2, "2026-09-01", e[0].CreatedAt.Add(time.Minute), s)}
				if match == "no-version" {
					v = v[1:]
				}
				out := tierCalculate(t, e, v)
				if out[e[1].ID].Amount.String() != "900.00" {
					t.Fatal(out)
				}
			})
		}
	}
}
func TestCommissionTiersPaidBase(t *testing.T) {
	e, s, _ := tierFixture("whole", "1", "1")
	for i := range e {
		e[i].Kind = "item"
		e[i].OrderItemID = new(uuid.New())
		e[i].CheckoutID = new(uuid.New())
		e[i].Base.Paid = amt("6000").Fen()
		e[i].Entries[0].PaidAmount = amt("6000")
	}
	s.Rules[0].Source = "item"
	s.Rules[0].Base = "paid"
	v := []dbgen.CommissionSchemeVersion{version(1, "2026-09-01", date("2026-09-01").Time, s)}
	out := tierCalculate(t, e, v)
	if out[e[0].ID].Amount.String() != "900.00" || out[e[0].ID].Explanation.Tier.Cumulative.String() != "12000.00" || out[e[0].ID].Explanation.Tier.BaseKind != "paid" {
		t.Fatal(out)
	}
}
func TestCommissionTiersReversal(t *testing.T) {
	for _, mode := range []string{"whole", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			e, s, v := tierFixture(mode, "6000", "6000")
			reversal := e[0]
			reversal.ID = uuid.New()
			reversal.Reverses = &e[0]
			reversal.VoidID = new(uuid.New())
			reversal.CreatedAt = e[1].CreatedAt.Add(time.Hour)
			all := append(slices.Clone(e), reversal)
			out := tierCalculate(t, all, v)
			want := "-700.00"
			before, after := "12000.00", "6000.00"
			if mode == "whole" {
				want = "-600.00"
			}
			c := out[reversal.ID]
			if c.Amount.String() != want || !c.Reversal || *c.Explanation.Reverses != e[0].ID || c.Explanation.Version.Version != 1 || out[e[0].ID].Amount.Fen()+out[e[1].ID].Amount.Fen()+c.Amount.Fen() != 60000 {
				t.Fatal(out)
			}
			if mode == "progressive" && (c.Explanation.Tier.Before.String() != before || c.Explanation.Tier.After.String() != after || len(c.Explanation.Tier.Parts) != 2 || c.Explanation.Tier.Parts[0].Base.String() != "-4000.00" || c.Explanation.Tier.Parts[1].Amount.String() != "-300.00") {
				t.Fatal(c)
			}
			// 下月冲回仍用原版本的档位，而非下月新方案；累计允许负数。
			reversal.BusinessDate = date("2026-11-01").Time
			reversal.CreatedAt = reversal.BusinessDate
			s.Rules[0].Cells[0].Tiers = &Tiers{Mode: mode, Levels: []TierLevel{{From: amt("0"), Percent: *pct("90")}, {From: amt("10000"), Percent: *pct("100")}}}
			v = append(v, version(2, "2026-11-01", date("2026-10-31").Time, s))
			out = tierCalculate(t, []performance.Unit{reversal}, v)
			c = out[reversal.ID]
			if c.Amount.String() != "-600.00" || c.Explanation.Version.Version != 1 || c.Explanation.Tier.Month != "2026-11" || c.Explanation.Tier.Level.Index != 0 {
				t.Fatal(out)
			}
			if mode == "whole" && c.Explanation.Tier.Cumulative.String() != "-6000.00" {
				t.Fatal(c)
			}
			if mode == "progressive" && (c.Explanation.Tier.Before.String() != "0.00" || c.Explanation.Tier.After.String() != "-6000.00") {
				t.Fatal(c)
			}
			// 整笔原笔与冲回在同月单独出现时净额为零。
			reversal.BusinessDate = e[0].BusinessDate
			out = tierCalculate(t, []performance.Unit{e[0], reversal}, v)
			if out[e[0].ID].Amount.Fen()+out[reversal.ID].Amount.Fen() != 0 {
				t.Fatal(out)
			}
		})
	}
}
func TestCommissionTiersRoundingAndOrder(t *testing.T) {
	e, s, _ := tierFixture("progressive", "0.01", "0.02")
	s.Rules[0].Cells[0].Tiers.Levels = []TierLevel{{From: amt("0"), Percent: *pct("50")}, {From: amt("0.02"), Percent: *pct("60")}}
	v := []dbgen.CommissionSchemeVersion{version(1, "2026-09-01", date("2026-09-01").Time, s)}
	// 输入倒序不影响累计；同日以写入时间排序，ID 刻意与写入时间相反。
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	e[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	out := tierCalculate(t, []performance.Unit{e[1], e[0]}, v)
	c := out[e[1].ID]
	if c.Amount.String() != "0.02" || len(c.Explanation.Tier.Parts) != 2 || c.Explanation.Tier.Parts[0].Amount.String() != "0.01" || c.Explanation.Tier.Parts[1].Amount.String() != "0.01" {
		t.Fatal(c)
	}
	e[0].CreatedAt = e[1].CreatedAt
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	e[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	out = tierCalculate(t, []performance.Unit{e[1], e[0]}, v)
	if out[e[1].ID].Explanation.Tier.Before.String() != "0.01" {
		t.Fatal(out)
	}
	e[0].BusinessDate = date("2026-10-14").Time
	e[0].CreatedAt = e[1].CreatedAt.Add(time.Hour)
	out = tierCalculate(t, e, v)
	if out[e[1].ID].Explanation.Tier.Before.String() != "0.01" {
		t.Fatal(out)
	}
}

func TestCommissionTiersBaseKindCacheIsolation(t *testing.T) {
	e, s, _ := tierFixture("whole", "1000", "2000")
	for i := range e {
		e[i].Kind = "item"
		e[i].OrderItemID = new(uuid.New())
		e[i].CheckoutID = new(uuid.New())
		e[i].Base.Paid = amt("6000").Fen()
		e[i].Entries[0].PaidAmount = amt("6000")
	}
	s.Rules[0].Source = "item"
	s.Rules[0].Base = "paid"
	v := []dbgen.CommissionSchemeVersion{version(1, "2026-09-01", date("2026-09-01").Time, s)}
	s.Rules[0].Base = "performance"
	v = append(v, version(2, "2026-09-01", e[0].CreatedAt.Add(time.Minute), s))
	// 同一次计算、同一员工来源营业月，两个版本分别累计 paid=12000 和 performance=3000。
	out := tierCalculate(t, e, v)
	if len(out) != 2 {
		t.Fatal(out)
	}
	for i, want := range []struct {
		kind, cumulative, amount string
		level                    int
		version                  int32
	}{
		{"paid", "12000.00", "900.00", 1, 1},
		{"performance", "3000.00", "200.00", 0, 2},
	} {
		c := out[e[i].ID]
		tier := c.Explanation.Tier
		if tier == nil || tier.Cumulative == nil {
			t.Fatalf("unit %d: missing tier cumulative: %+v", i, c)
		}
		if tier.BaseKind != want.kind || tier.Cumulative.String() != want.cumulative || c.Amount.String() != want.amount || tier.Level.Index != want.level || c.Explanation.Version.Version != want.version {
			t.Fatalf("unit %d: tier=%+v amount=%s version=%d, want %+v", i, tier, c.Amount.String(), c.Explanation.Version.Version, want)
		}
	}
}
