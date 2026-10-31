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
}

func TestCommissionBusinessDayBoundary(t *testing.T) {
	s := optest.OpenShop(t)
	// 本包不并行测试；把门店本地时刻固定在中午附近，避免 23:59 的最后一分钟使测试失效。
	location := settings.Location
	settings.Location = time.FixedZone("commission-test-noon", (12-time.Now().UTC().Hour())*60*60)
	t.Cleanup(func() { settings.Location = location })
	calendar := settings.Date(time.Now())
	business := calendar.AddDate(0, 0, -1)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'business_day_end','"23:59"',false,'"00:00"',true,$3,$4)`, s.Res.TenantID, s.Res.StoreID, business.AddDate(0, 0, -1), s.Res.OwnerID)
	dbtest.Commit(t, tx)
	if got := today(t, s); !got.Equal(business) || got.Equal(calendar) {
		t.Fatalf("business date %s, calendar %s, want %s", got, calendar, business)
	}
	accepted := save(t, s, business, emptyScheme())
	if accepted.Body["effective_from"] != business.Format(time.DateOnly) {
		t.Fatal(accepted)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": business.AddDate(0, 0, -1).Format(time.DateOnly), "scheme": emptyScheme()}), 409, "提成方案生效日期不能早于今天（"+business.Format(time.DateOnly)+"）")
}
