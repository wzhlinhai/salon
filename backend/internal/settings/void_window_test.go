package settings_test

import (
	"context"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"testing"
	"time"
)

func TestVoidWindowSetting(t *testing.T) {
	s := open(t)
	for _, ch := range s.Channels() {
		row := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyVoidWindowMinutes)
		if row["value"] != float64(0) || row["default"] != float64(0) {
			t.Fatal(row)
		}
	}
	for _, n := range []int{-1, 61} {
		s.change(s.owner, settings.KeyVoidWindowMinutes, n, day(0), 409)
	}
	s.change(s.owner, settings.KeyVoidWindowMinutes, 60, day(0), 200)
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		r, err := settings.Effective(ctx, q, s.Res.StoreID, settings.Date(time.Now()))
		if err == nil && r.VoidWindowMinutes() != 60 {
			t.Fatal(r)
		}
		return err
	})
}
