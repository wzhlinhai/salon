package commission_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCommissionValidation(t *testing.T) {
	s := optest.OpenShop(t)
	d := today(t, s).Format(time.DateOnly)
	cell := func() map[string]any { return map[string]any{"percent": "10"} }
	override := func() map[string]any {
		return map[string]any{"source": "item", "staff_id": s.Res.OwnerID, "percent": "10"}
	}
	for _, tc := range []struct {
		name, detail string
		change       func(map[string]any, map[string]any)
	}{
		{"duplicate source", "同一业绩来源只能有一条提成规则：项目业绩", func(sc, r map[string]any) { sc["rules"] = []any{r, r} }},
		{"consume base", "提成规则的计提基数无效：耗卡业绩", func(sc, r map[string]any) { r["source"] = "consume"; r["base"] = "paid" }},
		{"labor base", "提成规则的计提基数无效：手工费", func(sc, r map[string]any) { r["source"] = "labor"; r["base"] = "paid" }},
		{"sale base", "提成规则的计提基数无效：销售业绩", func(sc, r map[string]any) { r["source"] = "sale"; r["base"] = "paid" }},
		{"sale position", "销售业绩不能按岗位、指定或会员区分", func(sc, r map[string]any) { r["source"] = "sale"; r["by_position"] = true }},
		{"sale designated", "销售业绩不能按岗位、指定或会员区分", func(sc, r map[string]any) { r["source"] = "sale"; r["by_designated"] = true }},
		{"sale member", "销售业绩不能按岗位、指定或会员区分", func(sc, r map[string]any) { r["source"] = "sale"; r["by_member"] = true }},
		{"missing position", "提成规则格子的岗位、指定、会员必须与区分开关一致：项目业绩", func(sc, r map[string]any) { r["by_position"] = true }},
		{"extra position", "提成规则格子的岗位、指定、会员必须与区分开关一致：项目业绩", func(sc, r map[string]any) { c := cell(); c["position_id"] = uuid.New(); r["cells"] = []any{c} }},
		{"missing designated", "提成规则格子的岗位、指定、会员必须与区分开关一致：项目业绩", func(sc, r map[string]any) { r["by_designated"] = true }},
		{"extra designated", "提成规则格子的岗位、指定、会员必须与区分开关一致：项目业绩", func(sc, r map[string]any) { c := cell(); c["designated"] = false; r["cells"] = []any{c} }},
		{"missing member", "提成规则格子的岗位、指定、会员必须与区分开关一致：项目业绩", func(sc, r map[string]any) { r["by_member"] = true }},
		{"extra member", "提成规则格子的岗位、指定、会员必须与区分开关一致：项目业绩", func(sc, r map[string]any) { c := cell(); c["member"] = false; r["cells"] = []any{c} }},
		{"duplicate cell", "提成规则有重复格子：项目业绩", func(sc, r map[string]any) { r["cells"] = []any{cell(), cell()} }},
		{"cell both", "提成规则格子必须且只能填写比例或固定金额之一：项目业绩", func(sc, r map[string]any) { c := cell(); c["fixed_amount"] = "2"; r["cells"] = []any{c} }},
		{"cell neither", "提成规则格子必须且只能填写比例或固定金额之一：项目业绩", func(sc, r map[string]any) { r["cells"] = []any{map[string]any{}} }},
		{"no override target", "覆盖比例至少指定员工或项目：项目业绩", func(sc, r map[string]any) { o := override(); delete(o, "staff_id"); sc["overrides"] = []any{o} }},
		{"sale item override", "销售业绩不能设置项目覆盖比例", func(sc, r map[string]any) {
			r["source"] = "sale"
			o := override()
			o["source"] = "sale"
			o["item_id"] = uuid.New()
			sc["overrides"] = []any{o}
		}},
		{"override both", "覆盖比例必须且只能填写比例或固定金额之一：项目业绩", func(sc, r map[string]any) { o := override(); o["fixed_amount"] = "2"; sc["overrides"] = []any{o} }},
		{"override neither", "覆盖比例必须且只能填写比例或固定金额之一：项目业绩", func(sc, r map[string]any) { o := override(); delete(o, "percent"); sc["overrides"] = []any{o} }},
		{"duplicate override", "覆盖比例的来源、员工、项目组合重复：项目业绩", func(sc, r map[string]any) { sc["overrides"] = []any{override(), override()} }},
		{"no rule", "覆盖比例的业绩来源必须有提成规则：项目业绩", func(sc, r map[string]any) { sc["rules"] = []any{}; sc["overrides"] = []any{override()} }},
		{"duplicate excluded", "不计提成的员工不能重复", func(sc, r map[string]any) { sc["excluded_staff"] = []any{s.Res.OwnerID, s.Res.OwnerID} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, r := emptyScheme(), rule("item")
			r["cells"] = []any{cell()}
			sc["rules"] = []any{r}
			tc.change(sc, r)
			reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": d, "scheme": sc}), 409, tc.detail)
		})
	}
	for _, field := range []string{"percent", "fixed_amount"} {
		for _, where := range []string{"cell", "override"} {
			t.Run(field+where, func(t *testing.T) {
				value := "101"
				if field == "fixed_amount" {
					value = "-1"
				}
				sc, r := emptyScheme(), rule("item")
				sc["rules"] = []any{r}
				if where == "cell" {
					r["cells"] = []any{map[string]any{field: value}}
				} else {
					sc["overrides"] = []any{map[string]any{"source": "item", "staff_id": s.Res.OwnerID, field: value}}
				}
				reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": d, "scheme": sc}), 422, "validation failed")
			})
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM commission_scheme_versions").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
}

func TestCommissionSchemaEnums(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s).Format(time.DateOnly)
	for _, tc := range []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"rule source", func(sc, r map[string]any) { r["source"] = "unknown" }},
		{"item base", func(sc, r map[string]any) { r["base"] = "unknown" }},
		{"override source", func(sc, r map[string]any) {
			sc["overrides"] = []any{map[string]any{"source": "unknown", "staff_id": s.Res.OwnerID, "percent": "10"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, r := emptyScheme(), rule("item")
			sc["rules"] = []any{r}
			tc.change(sc, r)
			reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": day, "scheme": sc}), 422, "validation failed")
		})
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM commission_scheme_versions").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
}
