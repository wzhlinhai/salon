package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestGuestCountSetting(t *testing.T) {
	s := open(t)
	for _, ch := range s.Channels() {
		d := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{}), settings.KeyGuestCountSplit)
		if d["default"] != "first_slot" || d["value"] != "first_slot" || d["label"] != "客数归属" {
			t.Fatal(d)
		}
	}
	r := s.change(s.owner, settings.KeyGuestCountSplit, "bad", day(0), 409)
	if r.Detail() != "客数归属的做法无效" {
		t.Fatal(r)
	}
	s.change(s.owner, settings.KeyGuestCountSplit, "equal", day(0), 200)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		rules, err := settings.Effective(context.Background(), q, s.Res.StoreID, settings.Date(time.Now()))
		if err != nil {
			return err
		}
		if rules.GuestCountSplit() != "equal" {
			t.Fatal(rules.GuestCountSplit())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
