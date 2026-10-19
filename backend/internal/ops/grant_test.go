package ops_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func days(n int) *int { return &n }

func TestGrantCovers(t *testing.T) {
	cases := []struct {
		name      string
		have, ask ops.Grant
		want      bool
	}{
		{"same", ops.Grant{Scope: ops.ScopeSelf, WindowDays: days(3)}, ops.Grant{Scope: ops.ScopeSelf, WindowDays: days(3)}, true},
		{"store covers self", ops.Grant{Scope: ops.ScopeStore}, ops.Grant{Scope: ops.ScopeSelf}, true},
		{"self does not cover store", ops.Grant{Scope: ops.ScopeSelf}, ops.Grant{Scope: ops.ScopeStore}, false},
		{"longer window covers shorter", ops.Grant{WindowDays: days(7)}, ops.Grant{WindowDays: days(0)}, true},
		{"shorter window does not cover longer", ops.Grant{WindowDays: days(0)}, ops.Grant{WindowDays: days(7)}, false},
		{"unlimited covers any window", ops.Grant{}, ops.Grant{WindowDays: days(30)}, true},
		{"window does not cover unlimited", ops.Grant{WindowDays: days(30)}, ops.Grant{}, false},
	}
	for _, c := range cases {
		if got := c.have.Covers(c.ask); got != c.want {
			t.Errorf("%s: Covers = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGrantWidenTakesWidest(t *testing.T) {
	cases := []struct {
		name string
		a, b ops.Grant
		want ops.Grant
	}{
		{"scope", ops.Grant{Scope: ops.ScopeSelf}, ops.Grant{Scope: ops.ScopeStore}, ops.Grant{Scope: ops.ScopeStore}},
		{"window", ops.Grant{WindowDays: days(1)}, ops.Grant{WindowDays: days(3)}, ops.Grant{WindowDays: days(3)}},
		{"unlimited wins", ops.Grant{WindowDays: days(1)}, ops.Grant{}, ops.Grant{}},
		{"both", ops.Grant{Scope: ops.ScopeSelf, WindowDays: days(3)}, ops.Grant{Scope: ops.ScopeStore, WindowDays: days(1)},
			ops.Grant{Scope: ops.ScopeStore, WindowDays: days(3)}},
	}
	for _, c := range cases {
		for _, got := range []ops.Grant{c.a.Widen(c.b), c.b.Widen(c.a)} {
			if got.Scope != c.want.Scope || (got.WindowDays == nil) != (c.want.WindowDays == nil) ||
				(got.WindowDays != nil && *got.WindowDays != *c.want.WindowDays) {
				t.Errorf("%s: Widen = %+v, want %+v", c.name, got, c.want)
			}
		}
	}
}
