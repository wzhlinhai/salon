package commission_test

import (
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestCommissionEffectiveDateOrder(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	// 直接准备日期与保存次序相反的历史数据，让生效日期排序独立决定结果。
	for _, version := range []int{1, 2} {
		effective := day
		if version == 1 {
			effective = day.AddDate(0, 0, 2)
		}
		dbtest.Exec(t, tx, `INSERT INTO commission_scheme_versions(tenant_id,store_id,version,effective_from,scheme,saved_by) VALUES($1,$2,$3,$4,'{"rules":[],"overrides":[],"excluded_staff":[]}',$5)`, s.Res.TenantID, s.Res.StoreID, version, effective, s.Res.OwnerID)
	}
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_commission_scheme", map[string]any{"date": day.AddDate(0, 0, 3).Format(time.DateOnly)})
		if r.Status != 200 || r.Body["version"] != float64(1) {
			t.Fatal(ch.Name, r)
		}
	}
	// 保留先见新日期的断言，再追加先见旧日期、后见获胜日期的真实加载路径。
	save(t, s, day.AddDate(0, 0, 3), emptyScheme())
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_commission_scheme", map[string]any{"date": day.AddDate(0, 0, 4).Format(time.DateOnly)})
		if r.Status != 200 || r.Body["version"] != float64(3) {
			t.Fatal(ch.Name, r)
		}
	}

}

func TestCommissionBusinessDayBoundary(t *testing.T) {
	s := optest.OpenShop(t)
	calendar := settings.Date(time.Now())
	business := calendar.AddDate(0, 0, -1)
	s.SetNow(business.AddDate(0, 0, -2).Add(12 * time.Hour))
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": business.AddDate(0, 0, -1).Format(time.DateOnly)}, 200)
	s.SetNow(time.Date(calendar.Year(), calendar.Month(), calendar.Day(), 1, 30, 0, 0, settings.Location))
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_settings", map[string]any{})
		if r.Status != 200 || r.Body["business_date"] != business.Format(time.DateOnly) || r.Body["business_date"] == calendar.Format(time.DateOnly) {
			t.Fatalf("%s: business date %v, calendar %s, want %s", ch.Name, r, calendar, business)
		}
	}
	accepted := save(t, s, business, emptyScheme())
	if accepted.Body["effective_from"] != business.Format(time.DateOnly) {
		t.Fatal(accepted)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": business.AddDate(0, 0, -1).Format(time.DateOnly), "scheme": emptyScheme()}), 409, "提成方案生效日期不能早于今天（"+business.Format(time.DateOnly)+"）")
}
