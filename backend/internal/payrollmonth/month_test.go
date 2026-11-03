package payrollmonth

import (
	"testing"
	"time"
)

func TestMonth(t *testing.T) {
	for _, tc := range []struct{ month, last, prev string }{
		{"2026-01", "2026-01-31", "2025-12"},
		{"2026-02", "2026-02-28", "2026-01"},
		{"2024-02", "2024-02-29", "2024-01"},
		{"2026-04", "2026-04-30", "2026-03"},
		{"2026-12", "2026-12-31", "2026-11"},
	} {
		t.Run(tc.month, func(t *testing.T) {
			m, err := Parse(tc.month)
			if err != nil {
				t.Fatal(err)
			}
			if m.Before(m) || m.Before(m.Prev()) || !m.Prev().Before(m) || m.String() != tc.month || m.First().Format(time.DateOnly) != tc.month+"-01" || m.Last().Format(time.DateOnly) != tc.last || m.Prev().String() != tc.prev {
				t.Fatal(m, m.First(), m.Last(), m.Prev())
			}
			day := time.Date(m.First().Year(), m.First().Month(), 23, 17, 45, 30, 123, time.FixedZone("营业日", 8*3600))
			if Of(day) != m || m.First().Location() != time.UTC {
				t.Fatal(Of(day), m)
			}
			if got := map[Month]string{m: tc.month}[Of(day)]; got != tc.month {
				t.Fatal(got)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	for _, s := range []string{"", "2026", "2026-1", "2026-00", "2026-13", "26-01", "2026-01-01", " 2026-01"} {
		t.Run(s, func(t *testing.T) {
			_, err := Parse(s)
			if err == nil || err.Error() != "结算月的格式是 YYYY-MM" {
				t.Fatalf("Parse(%q) = %v", s, err)
			}
		})
	}
}

func TestLockability(t *testing.T) {
	opened := fixtureMonth(t, "2026-01")
	current := fixtureMonth(t, "2026-04")
	for _, tc := range []struct {
		name, month            string
		locked, previous, want bool
	}{
		{"开店月已结束", "2026-01", false, false, true},
		{"前月未锁", "2026-02", false, false, false},
		{"前月已锁", "2026-02", false, true, true},
		{"当前月", "2026-04", false, true, false},
		{"已锁", "2026-02", true, true, false},
		{"未来月", "2026-05", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureMonth(t, tc.month)
			if got := lockable(m, opened, current, tc.locked, tc.previous); got != tc.want {
				t.Fatalf("lockable = %v, want %v", got, tc.want)
			}
		})
	}
}

func fixtureMonth(t *testing.T, s string) Month {
	t.Helper()
	m, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOfMonthEdges(t *testing.T) {
	for _, tc := range []struct{ day, month string }{
		{"2026-03-01T00:30:00+08:00", "2026-03"},
		{"2026-04-30T23:30:00-05:00", "2026-04"},
		{"2026-01-01T00:30:00+08:00", "2026-01"},
		{"2026-12-31T23:30:00-05:00", "2026-12"},
	} {
		t.Run(tc.day, func(t *testing.T) {
			day, err := time.Parse(time.RFC3339, tc.day)
			if err != nil {
				t.Fatal(err)
			}
			want := fixtureMonth(t, tc.month)
			if got := Of(day); got != want || got == Of(day.UTC()) {
				t.Fatalf("Of(%s) = %s, want %s (UTC instant %s)", day, got, want, day.UTC())
			}
		})
	}
}
