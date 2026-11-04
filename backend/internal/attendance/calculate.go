package attendance

import (
	"slices"
	"strings"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/inforce"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// calculate 按营业日现算；输入均已按员工和月份分组，不访问数据库。
func calculate(summary AttendanceSummary, punches []Punch, approvals []AttendanceApproval, timeline inforce.Timeline[StaffShift], grace map[string]int, today time.Time) StaffAttendance {
	days := map[string]*AttendanceDay{}
	dayFor := func(date string) *AttendanceDay {
		if days[date] == nil {
			days[date] = &AttendanceDay{BusinessDate: date, Punches: []Punch{}}
		}
		return days[date]
	}
	for _, p := range punches {
		d := dayFor(p.BusinessDate)
		d.Punches = append(d.Punches, p)
	}
	for _, a := range approvals {
		if a.RevokedAt == nil {
			dayFor(a.BusinessDate).Approval = &a
		}
	}
	out := StaffAttendance{hasHistory: len(punches) > 0 || len(approvals) > 0, Summary: summary, Days: []AttendanceDay{}}
	for date, d := range days {
		day, _ := time.Parse(settings.DateLayout, date)
		if c, ok := timeline.At(day); ok {
			d.Shift = c.Value.Shift
		}
		slices.SortFunc(d.Punches, func(a, b Punch) int {
			if c := a.At.Compare(b.At); c != 0 {
				return c
			}
			return strings.Compare(a.ID.String(), b.ID.String())
		})
		valid := []Punch{}
		for _, p := range d.Punches {
			if !p.Voided {
				valid = append(valid, p)
			}
		}
		if len(valid) > 0 {
			out.Summary.AttendanceDays++
			first := valid[0].At
			d.ClockIn = &first
			if len(valid) >= 2 {
				last := valid[len(valid)-1].At
				d.ClockOut = &last
			}
			d.Missing = len(valid) == 1 && date < today.Format(settings.DateLayout)
			if d.Shift != nil {
				start, _ := time.ParseInLocation("2006-01-02 15:04", date+" "+d.Shift.StartTime, settings.Location)
				end, _ := time.ParseInLocation("2006-01-02 15:04", date+" "+d.Shift.EndTime, settings.Location)
				firstMinute := first.In(settings.Location).Truncate(time.Minute)
				if firstMinute.After(start.Add(time.Duration(grace[date]) * time.Minute)) {
					d.Late = true
					d.LateMinutes = int(firstMinute.Sub(start) / time.Minute)
				}
				if d.ClockOut != nil {
					lastMinute := d.ClockOut.In(settings.Location).Truncate(time.Minute)
					if lastMinute.Before(end.Add(-time.Duration(grace[date]) * time.Minute)) {
						d.Early = true
						d.EarlyMinutes = int(end.Sub(lastMinute) / time.Minute)
					}
				}
			}
		}
		if d.Approval != nil {
			out.Summary.ApprovedCount++
		} else {
			if d.Late {
				out.Summary.LateCount++
				out.Summary.LateMinutes += d.LateMinutes
			}
			if d.Early {
				out.Summary.EarlyCount++
				out.Summary.EarlyMinutes += d.EarlyMinutes
			}
			if d.Missing {
				out.Summary.MissingCount++
			}
		}
		out.Days = append(out.Days, *d)
	}
	slices.SortFunc(out.Days, func(a, b AttendanceDay) int { return strings.Compare(a.BusinessDate, b.BusinessDate) })
	return out
}
