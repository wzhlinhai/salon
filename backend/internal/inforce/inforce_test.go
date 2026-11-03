package inforce

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func id(n byte) uuid.UUID { return uuid.UUID{15: n} }

var saved = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// 10-05 有三次保存：保存时刻最晚的两次时刻相同，ID 大的为准；10-10 的修改保存得比 10-05 早，仍按生效日期在后。
func fixture() []Change[string] {
	return []Change[string]{
		{From: day("2026-10-05"), SavedAt: saved, ID: id(2), Value: "same-time-larger-id"},
		{From: day("2026-10-05"), SavedAt: saved, ID: id(1), Value: "same-time-smaller-id"},
		{From: day("2026-10-05"), SavedAt: saved.Add(-time.Hour), ID: id(9), Value: "earlier-saved"},
		{From: day("2026-10-01"), SavedAt: saved.Add(time.Hour), ID: id(3), Value: "first"},
		{From: day("2026-10-10"), SavedAt: saved.Add(-2 * time.Hour), ID: id(4), Value: "later-date"},
	}
}

// orders 返回同一组修改的几种输入顺序，选择结果不能依赖顺序。
func orders() map[string][]Change[string] {
	reversed := fixture()
	slices.Reverse(reversed)
	rotated := fixture()
	rotated = append(rotated[2:], rotated[:2]...)
	return map[string][]Change[string]{"given": fixture(), "reversed": reversed, "rotated": rotated}
}

func TestAt(t *testing.T) {
	for name, changes := range orders() {
		timeline := New(changes)
		for _, tc := range []struct {
			day  string
			want string
		}{
			{"2026-09-30", ""},
			{"2026-10-01", "first"},
			{"2026-10-04", "first"},
			{"2026-10-05", "same-time-larger-id"},
			{"2026-10-09", "same-time-larger-id"},
			{"2026-10-10", "later-date"},
			{"2027-01-01", "later-date"},
		} {
			got, ok := timeline.At(day(tc.day))
			if got.Value != tc.want || ok != (tc.want != "") {
				t.Errorf("%s: At(%s) = %q, %v; want %q", name, tc.day, got.Value, ok, tc.want)
			}
		}
	}
}

func TestAtSavedAtBeforeID(t *testing.T) {
	// 保存时刻晚的即使 ID 小也为准。
	timeline := New([]Change[string]{
		{From: day("2026-10-05"), SavedAt: saved, ID: id(1), Value: "later-saved"},
		{From: day("2026-10-05"), SavedAt: saved.Add(-time.Second), ID: id(2), Value: "earlier-saved"},
	})
	if got, _ := timeline.At(day("2026-10-05")); got.Value != "later-saved" {
		t.Fatal(got)
	}
}

func TestAfter(t *testing.T) {
	for name, changes := range orders() {
		timeline := New(changes)
		for _, tc := range []struct {
			day  string
			want []string
		}{
			{"2026-09-30", []string{"first", "same-time-larger-id", "later-date"}},
			{"2026-10-01", []string{"same-time-larger-id", "later-date"}},
			{"2026-10-05", []string{"later-date"}},
			{"2026-10-10", nil},
		} {
			var got []string
			for _, c := range timeline.After(day(tc.day)) {
				got = append(got, c.Value)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s: After(%s) = %q; want %q", name, tc.day, got, tc.want)
			}
		}
	}
}

func TestEmpty(t *testing.T) {
	var timeline Timeline[string]
	if got, ok := timeline.At(day("2026-10-05")); ok || got.Value != "" {
		t.Fatal(got, ok)
	}
	if got := New[string](nil).After(day("2026-10-05")); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestGroup(t *testing.T) {
	changes := append(fixture(), Change[string]{From: day("2026-10-02"), SavedAt: saved, ID: id(5), Value: "other-key"})
	groups := Group(changes, func(c Change[string]) bool { return c.Value == "other-key" })
	if len(groups) != 2 {
		t.Fatal(groups)
	}
	if got, _ := groups[false].At(day("2026-10-05")); got.Value != "same-time-larger-id" {
		t.Fatal(got)
	}
	if got, _ := groups[true].At(day("2026-10-05")); got.Value != "other-key" {
		t.Fatal(got)
	}
	if _, ok := groups[true].At(day("2026-10-01")); ok {
		t.Fatal("other key in force before its effective date")
	}
}

func TestAtSeqBeforeSavedAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  int64
		want string
	}{
		{"higher-seq", 2, "higher-seq-earlier-time"},
		{"equal-seq", 1, "later-time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timeline := New([]Change[string]{
				{From: day("2026-10-05"), Seq: tc.seq, SavedAt: saved.Add(-time.Hour), ID: id(1), Value: "higher-seq-earlier-time"},
				{From: day("2026-10-05"), Seq: 1, SavedAt: saved, ID: id(2), Value: "later-time"},
			})
			if got, ok := timeline.At(day("2026-10-05")); !ok || got.Value != tc.want {
				t.Fatal(got, ok)
			}
		})
	}
}
