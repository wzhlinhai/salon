package attendance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/inforce"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestCalculateAttendance(t *testing.T) {
	day := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	shift := &Shift{StartTime: "09:00", EndTime: "18:00"}
	timeline := inforce.New([]inforce.Change[StaffShift]{{From: day, Value: StaffShift{Shift: shift}}})
	for _, tc := range []struct {
		name                                                             string
		times                                                            []string
		grace                                                            int
		today, noShift, voidFirst, approved                              bool
		days, late, lateMinutes, early, earlyMinutes, missing, approvals int
	}{
		{name: "on time", times: []string{"09:00:00", "18:00:00"}, days: 1},
		{name: "late one", times: []string{"09:01:00", "18:00:00"}, days: 1, late: 1, lateMinutes: 1},
		{name: "within grace", times: []string{"09:05:00", "18:00:00"}, grace: 5, days: 1},
		{name: "beyond grace", times: []string{"09:06:00", "18:00:00"}, grace: 5, days: 1, late: 1, lateMinutes: 6},
		{name: "seconds dropped", times: []string{"09:00:59", "18:00:00"}, days: 1},
		{name: "early", times: []string{"09:00:00", "17:54:59"}, grace: 5, days: 1, early: 1, earlyMinutes: 6},
		{name: "early within grace", times: []string{"09:00:00", "17:55:00"}, grace: 5, days: 1},
		{name: "single past", times: []string{"09:01:00"}, days: 1, late: 1, lateMinutes: 1, missing: 1},
		{name: "single today", times: []string{"09:00:00"}, today: true, days: 1},
		{name: "no shift", times: []string{"10:00:00", "17:00:00"}, noShift: true, days: 1},
		{name: "voided ignored", times: []string{"08:00:00", "09:01:00", "18:00:00"}, voidFirst: true, days: 1, late: 1, lateMinutes: 1},
		{name: "voided only", times: []string{"09:01:00"}, voidFirst: true},
		{name: "approved", times: []string{"09:01:00"}, approved: true, days: 1, approvals: 1},
		{name: "middle ignored", times: []string{"09:00:00", "12:00:00", "18:00:00"}, days: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			punches := []Punch{}
			for i, at := range tc.times {
				ts, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-06-12 "+at, settings.Location)
				if err != nil {
					t.Fatal(err)
				}
				punches = append(punches, Punch{ID: uuid.New(), At: ts, BusinessDate: "2026-06-12", Voided: i == 0 && tc.voidFirst})
			}
			approvals := []AttendanceApproval{}
			if tc.approved {
				approvals = append(approvals, AttendanceApproval{BusinessDate: "2026-06-12"})
			}
			tl := timeline
			if tc.noShift {
				tl = inforce.New([]inforce.Change[StaffShift]{})
			}
			today := day.AddDate(0, 0, 1)
			if tc.today {
				today = day
			}
			got := calculate(AttendanceSummary{}, punches, approvals, tl, map[string]int{"2026-06-12": tc.grace}, today)
			s := got.Summary
			if s.AttendanceDays != tc.days || s.LateCount != tc.late || s.LateMinutes != tc.lateMinutes || s.EarlyCount != tc.early || s.EarlyMinutes != tc.earlyMinutes || s.MissingCount != tc.missing || s.ApprovedCount != tc.approvals {
				t.Fatalf("%+v", s)
			}
			if len(got.Days) != 1 || len(got.Days[0].Punches) != len(tc.times) {
				t.Fatal(got)
			}
			d := got.Days[0]
			if len(tc.times) == 1 && d.ClockOut != nil {
				t.Fatal(d)
			}
			if tc.approved && (!d.Late || !d.Missing) {
				t.Fatal("approval must retain raw anomalies", d)
			}
		})
	}
}

func TestCalculateAttendanceMultiDay(t *testing.T) {
	shiftA := &Shift{StartTime: "09:00", EndTime: "18:00"}
	shiftB := &Shift{StartTime: "10:00", EndTime: "19:00"}
	timeline := inforce.New([]inforce.Change[StaffShift]{
		{From: time.Date(2026, 6, 1, 0, 0, 0, 0, settings.Location), Value: StaffShift{Shift: shiftA}},
		{From: time.Date(2026, 6, 15, 0, 0, 0, 0, settings.Location), Value: StaffShift{Shift: shiftB}},
	})
	punch := func(day, hour, minute int) Punch {
		return Punch{
			ID:           uuid.New(),
			At:           time.Date(2026, 6, day, hour, minute, 0, 0, settings.Location),
			BusinessDate: time.Date(2026, 6, day, 0, 0, 0, 0, settings.Location).Format(settings.DateLayout),
		}
	}
	punches := []Punch{
		punch(20, 19, 0),
		punch(20, 9, 30),
		punch(3, 18, 0),
		punch(3, 9, 30),
		punch(10, 18, 0),
		punch(10, 9, 0),
	}
	want := []struct {
		date        string
		shift       *Shift
		late        bool
		lateMinutes int
		first, last time.Time
	}{
		{"2026-06-03", shiftA, true, 30, punch(3, 9, 30).At, punch(3, 18, 0).At},
		{"2026-06-10", shiftA, false, 0, punch(10, 9, 0).At, punch(10, 18, 0).At},
		{"2026-06-20", shiftB, false, 0, punch(20, 9, 30).At, punch(20, 19, 0).At},
	}
	today := time.Date(2026, 6, 30, 0, 0, 0, 0, settings.Location)
	for range 32 {
		got := calculate(AttendanceSummary{}, punches, nil, timeline, map[string]int{}, today)
		if len(got.Days) != len(want) {
			t.Fatal(got.Days)
		}
		for i, w := range want {
			day := got.Days[i]
			if day.BusinessDate != w.date || day.Shift != w.shift || day.Late != w.late || day.LateMinutes != w.lateMinutes {
				t.Fatalf("day %d: %+v", i, day)
			}
			for j := 1; j < len(day.Punches); j++ {
				if day.Punches[j-1].At.After(day.Punches[j].At) {
					t.Fatalf("punches not sorted: %+v", day.Punches)
				}
			}
			if day.ClockIn == nil || !day.ClockIn.Equal(w.first) || day.ClockOut == nil || !day.ClockOut.Equal(w.last) {
				t.Fatalf("day %d clock: %+v", i, day)
			}
		}
	}
}
