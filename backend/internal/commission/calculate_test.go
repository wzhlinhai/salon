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
func calcFixture() ([]dbgen.PerformanceEntry, map[lineKey]lineFact, CommissionScheme, []dbgen.CommissionSchemeVersion) {
	staff, item, line, checkout, position := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	entries := []dbgen.PerformanceEntry{}
	for _, slot := range []int16{1, 2} {
		for i, method := range []string{"cash", "card_principal", "card_bonus", "red_packet"} {
			paid := []string{"14", "21", "28", "7"}[i]
			amount := []string{"14", "10.50", "7", "0.70"}[i]
			if slot == 2 {
				paid = []string{"6", "9", "12", "3"}[i]
				amount = []string{"6", "4.50", "3", "0.30"}[i]
			}
			entries = append(entries, dbgen.PerformanceEntry{ID: uuid.New(), Kind: "item", BusinessDate: date("2026-10-31"), CheckoutID: &checkout, OrderItemID: &line, StaffID: &staff, PositionID: &position, Slot: new(slot), Member: new(true), Method: method, PaidAmount: amt(paid), Amount: amt(amount), Ratio: []int16{100, 50, 25, 10}[i], CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Valid: true}})
		}
	}
	facts := map[lineKey]lineFact{{line, checkout}: {itemID: item, price: amt("123.45"), paid: "100.00", designated: true}}
	s := CommissionScheme{Rules: []Rule{{Source: "item", Base: "performance", Cells: []Cell{{Percent: pct("10")}}}}}
	versions := []dbgen.CommissionSchemeVersion{version(1, "2026-10-31", entries[0].CreatedAt.Time.Add(-time.Hour), s)}
	return entries, facts, s, versions
}
func runCalc(t *testing.T, e []dbgen.PerformanceEntry, f map[lineKey]lineFact, s CommissionScheme) []Commission {
	t.Helper()
	v := version(1, "2026-10-31", e[0].CreatedAt.Time.Add(-time.Hour), s)
	out, err := calculate(e, nil, f, []dbgen.CommissionSchemeVersion{v})
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
			e, f, s, _ := calcFixture()
			s.Rules[0].Base = tc.base
			out := runCalc(t, e, f, s)
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
	// 每一种现金方式均计入现金业绩；红包不得混入现金或卡金。
	for _, method := range []string{"cash", "unionpay", "wechat", "alipay", "debt"} {
		t.Run(method, func(t *testing.T) {
			e, f, s, _ := calcFixture()
			e = e[:1]
			e[0].Method = method
			s.Rules[0].Base = "cash_performance"
			out := runCalc(t, e, f, s)
			if len(out) != 1 || out[0].Amount.String() != "1.40" {
				t.Fatal(out)
			}
		})
	}
	for _, source := range []string{"consume", "labor", "sale"} {
		t.Run(source, func(t *testing.T) {
			e, f, s, _ := calcFixture()
			e = e[:1]
			e[0].Kind = source
			s.Rules[0].Source = source
			out := runCalc(t, e, f, s)
			if len(out) != 1 || out[0].Amount.String() != "1.40" {
				t.Fatal(out)
			}
		})
	}
}
func TestCommissionFixedUnit(t *testing.T) {
	e, f, s, _ := calcFixture()
	e = e[:3]
	s.Rules[0].Cells = []Cell{{FixedAmount: new(amt("2.34"))}}
	out := runCalc(t, e, f, s)
	if len(out) != 1 || out[0].Amount.String() != "2.34" || len(out[0].Explanation.Entries) != 3 {
		t.Fatal(out)
	}
	for i := range e {
		e[i].Amount = amt("0")
	}
	out = runCalc(t, e, f, s)
	if len(out) != 1 || out[0].Amount.String() != "2.34" {
		t.Fatal(out)
	}
	s.Rules[0].Cells = []Cell{{Percent: pct("50")}}
	out = runCalc(t, e, f, s)
	if len(out) != 1 || out[0].Amount.String() != "0.00" {
		t.Fatal(out)
	}
	e = e[:1]
	e[0].Amount = amt("0.01")
	out = runCalc(t, e, f, s)
	if out[0].Amount.String() != "0.01" {
		t.Fatal(out)
	}
}
func TestCommissionMatchOrder(t *testing.T) {
	e, f, s, _ := calcFixture()
	e = e[:1]
	item := f[lineKey{*e[0].OrderItemID, *e[0].CheckoutID}].itemID
	overrides := []Override{{Source: "item", StaffID: e[0].StaffID, Percent: pct("20")}, {Source: "item", ItemID: &item, Percent: pct("30")}, {Source: "item", StaffID: e[0].StaffID, ItemID: &item, Percent: pct("40")}}
	for _, tc := range []struct {
		n            int
		kind, amount string
	}{{3, "override_staff_item", "5.60"}, {2, "override_item", "4.20"}, {1, "override_staff", "2.80"}, {0, "cell", "1.40"}} {
		t.Run(tc.kind, func(t *testing.T) {
			s.Overrides = overrides[:tc.n]
			out := runCalc(t, e, f, s)
			if len(out) != 1 || out[0].Amount.String() != tc.amount || out[0].Explanation.Match.Kind != tc.kind {
				t.Fatal(out)
			}
		})
	}
	s.Rules[0].Cells = nil
	s.Overrides = overrides[:1]
	out := runCalc(t, e, f, s)
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
	out = runCalc(t, e, f, s)
	if len(out) != 1 || out[0].Explanation.Match.Kind != "override_staff" || out[0].Amount.String() != "2.80" {
		t.Fatal(out)
	}
	s.Overrides = overrides[1:]
	out = runCalc(t, e, f, s)
	if len(out) != 1 || out[0].Explanation.Match.Kind != "cell" {
		t.Fatal(out)
	}
}
func TestCommissionCellKeysAndSkips(t *testing.T) {
	for _, key := range []string{"position", "designated", "member"} {
		t.Run(key, func(t *testing.T) {
			e, f, s, _ := calcFixture()
			e = e[:1]
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
			out := runCalc(t, e, f, s)
			if len(out) != 1 || out[0].Amount.String() != "1.40" {
				t.Fatal(out)
			}
			s.Rules[0].Cells = []Cell{wrong}
			if out := runCalc(t, e, f, s); len(out) != 0 {
				t.Fatal(out)
			}
		})
	}
	for _, skip := range []string{"excluded", "version", "store"} {
		t.Run(skip, func(t *testing.T) {
			e, f, s, v := calcFixture()
			switch skip {
			case "excluded":
				s.ExcludedStaff = []uuid.UUID{*e[0].StaffID}
				v = []dbgen.CommissionSchemeVersion{version(1, "2026-10-31", e[0].CreatedAt.Time.Add(-time.Hour), s)}
			case "version":
				v = nil
			case "store":
				for i := range e {
					e[i].StaffID = nil
				}
			}
			out, err := calculate(e, nil, f, v)
			if err != nil || len(out) != 0 {
				t.Fatal(out, err)
			}
		})
	}
}
func TestCommissionUnitVersionsAndReversal(t *testing.T) {
	e, f, s, v := calcFixture()
	e = e[:4]
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	e[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	newer := s
	newer.Rules = []Rule{{Source: "item", Base: "performance", Cells: []Cell{{Percent: pct("90")}}}}
	v = append(v, version(2, "2026-10-31", e[0].CreatedAt.Time.Add(time.Second), newer), version(3, "2026-11-01", e[0].CreatedAt.Time.Add(-time.Second), newer))
	// 最早写入时间决定版本，即使之后的支付记录晚于新方案。
	e[1].CreatedAt.Time = e[0].CreatedAt.Time.Add(time.Minute)
	out, err := calculate(e, nil, f, v)
	if err != nil || len(out) != 1 || out[0].Amount.String() != "3.22" || out[0].Explanation.Version.Version != 1 {
		t.Fatal(out, err)
	}
	original := out[0]
	void := uuid.New()
	reversals := append([]dbgen.PerformanceEntry{}, e...)
	for i := range reversals {
		reversals[i].ID = uuid.New()
		reversals[i].ReversesID = &e[i].ID
		reversals[i].VoidID = &void
		reversals[i].BusinessDate = date("2026-11-02")
		reversals[i].CreatedAt.Time = e[i].CreatedAt.Time.Add(48 * time.Hour)
	}
	out, err = calculate(reversals, e, f, v)
	if err != nil || len(out) != 1 || out[0].Amount.String() != "-3.22" || out[0].Explanation.Version.Version != 1 || *out[0].Explanation.Reverses != original.ID || out[0].Explanation.Match.Cell.Percent.String() != "10.00" || out[0].BusinessDate != "2026-11-02" {
		t.Fatal(out, err)
	}
	// 原笔与冲回同范围时，原记录不能被重复计入基数。
	both := append(append([]dbgen.PerformanceEntry{}, e...), reversals...)
	out, err = calculate(both, e, f, v)
	if err != nil || len(out) != 2 || out[0].Amount.String() != "-3.22" || out[1].Amount.String() != "3.22" {
		t.Fatal(out, err)
	}
}
func TestSelectCommissionVersion(t *testing.T) {
	_, _, s, _ := calcFixture()
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
	e, f, s, v := calcFixture()
	// UUID 次序与写入次序刻意相反；单位用最小 ID，排序用最早写入时刻。
	e[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	e[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	e[0].CreatedAt.Time = e[0].CreatedAt.Time.Add(-time.Minute)
	out, err := calculate(e, nil, f, v)
	if err != nil || len(out) != 2 || *out[0].Explanation.Origin.Slot != 2 || out[1].ID != e[1].ID {
		t.Fatal(out, err)
	}
	e = e[:1]
	for _, o := range []Override{{Source: "sale", StaffID: e[0].StaffID, Percent: pct("90")}, {Source: "item", StaffID: new(uuid.New()), Percent: pct("90")}, {Source: "item", ItemID: new(uuid.New()), Percent: pct("90")}} {
		s.Overrides = []Override{o}
		out := runCalc(t, e, f, s)
		if len(out) != 1 || out[0].Amount.String() != "1.40" || out[0].Explanation.Match.Kind != "cell" {
			t.Fatal(out)
		}
	}
	// 一条销售业绩就是一笔；多来源不能混合，也不能跨结账合并。
	e, f, s, _ = calcFixture()
	e = e[:2]
	e[1].Kind = "sale"
	e[1].Member = nil
	e[1].CheckoutID = nil
	e[1].OrderItemID = nil
	e[1].Slot = nil
	e[1].PositionID = nil
	s.Rules = append(s.Rules, Rule{Source: "sale", Base: "performance", Cells: []Cell{{FixedAmount: new(amt("2"))}}})
	out = runCalc(t, e, f, s)
	if len(out) != 2 {
		t.Fatal(out)
	}
	for _, c := range out {
		if c.Source == "sale" && (c.Amount.String() != "2.00" || c.Explanation.Member != nil || c.Explanation.Designated != nil) {
			t.Fatal(c)
		}
	}
}
