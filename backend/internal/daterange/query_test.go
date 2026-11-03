package daterange_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestParseDates(t *testing.T) {
	for _, tc := range []struct{ name, from, to, message string }{
		{"invalid from", "2026-02-30", "2026-04-03", "请填写有效的营业日"},
		{"invalid to", "2026-01-01", "bad", "请填写有效的营业日"},
		{"order", "2026-02-01", "2026-01-01", "开始营业日不能晚于结束营业日"},
		{"93 days", "2026-01-01", "2026-04-04", "查询范围最多 92 天"},
		{"92 days", "2026-01-01", "2026-04-03", ""},
		{"same day", "2026-01-01", "2026-01-01", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := daterange.Parse(ops.Caller{}, perm.PerformanceView, tc.from, tc.to, nil, 0)
			if tc.message != "" {
				var rule *ops.RuleError
				if !errors.As(err, &rule) || rule.Message != tc.message {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || q.From.Format(time.DateOnly) != tc.from || q.To.Format(time.DateOnly) != tc.to {
				t.Fatal(q, err)
			}
		})
	}
}

func TestParseScope(t *testing.T) {
	self, other := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name            string
		scope           ops.Scope
		requested, want *uuid.UUID
		empty           bool
	}{
		{"store none", ops.ScopeStore, nil, nil, false},
		{"store staff", ops.ScopeStore, &other, &other, false},
		{"self none", ops.ScopeSelf, nil, &self, false},
		{"self self", ops.ScopeSelf, &self, &self, false},
		{"self other", ops.ScopeSelf, &other, &self, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := ops.Caller{StaffID: self, Permissions: map[ops.Permission]ops.Grant{perm.PerformanceView: {Scope: tc.scope}}}
			q, err := daterange.Parse(caller, perm.PerformanceView, "2026-01-01", "2026-01-01", tc.requested, 0)
			if err != nil || q.Empty != tc.empty || (q.StaffID == nil) != (tc.want == nil) {
				t.Fatal(q, err)
			}
			if tc.want != nil && *q.StaffID != *tc.want {
				t.Fatal(q)
			}
		})
	}
}

func TestParseLimit(t *testing.T) {
	for _, tc := range []struct{ in, want int32 }{{0, 50}, {1, 1}, {200, 200}} {
		q, err := daterange.Parse(ops.Caller{}, perm.PerformanceView, "2026-01-01", "2026-01-01", nil, tc.in)
		if err != nil || q.Limit != tc.want {
			t.Fatal(q, err)
		}
	}
}
