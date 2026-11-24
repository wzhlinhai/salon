package customers_test

import (
	"strings"
	"testing"
)

func TestCustomerHairProfile(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"name": "发质顾客"})
	full := map[string]any{
		"id": a["id"], "name": "发质顾客", "note": "备注",
		"hair_texture": "细软", "preferences": "不要吹太干", "dislikes": "怕烫",
	}
	saved := s.call("update_customer_profile", full, 200)
	assertEqual(t, saved["hair_texture"], "细软")
	assertEqual(t, saved["preferences"], "不要吹太干")
	assertEqual(t, saved["dislikes"], "怕烫")
	fetched := s.get(a["id"])
	assertEqual(t, fetched["hair_texture"], "细软")
	assertEqual(t, fetched["preferences"], "不要吹太干")
	assertEqual(t, fetched["dislikes"], "怕烫")

	entries := s.call("list_audit_log", map[string]any{"target_id": a["id"]}, 200)["entries"].([]any)
	latest := entries[0].(map[string]any)
	assertEqual(t, latest["action"], "update_customer_profile")
	before := latest["before"].(map[string]any)
	after := latest["after"].(map[string]any)
	assertEqual(t, before["hair_texture"], nil)
	assertEqual(t, before["preferences"], nil)
	assertEqual(t, before["dislikes"], nil)
	assertEqual(t, after["hair_texture"], "细软")
	assertEqual(t, after["preferences"], "不要吹太干")
	assertEqual(t, after["dislikes"], "怕烫")

	cleared := s.call("update_customer_profile", map[string]any{"id": a["id"]}, 200)
	assertEqual(t, cleared["hair_texture"], nil)
	assertEqual(t, cleared["preferences"], nil)
	assertEqual(t, cleared["dislikes"], nil)
	assertEqual(t, cleared["note"], nil)
}

func TestCustomerHairProfileLengths(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"name": "长度顾客"})
	for _, tc := range []struct {
		field string
		value string
	}{
		{"hair_texture", strings.Repeat("发", 41)},
		{"preferences", strings.Repeat("喜", 201)},
		{"dislikes", strings.Repeat("忌", 201)},
	} {
		t.Run(tc.field, func(t *testing.T) {
			r := s.HTTPWithToken(t, s.Owner, "update_customer_profile", map[string]any{"id": a["id"], tc.field: tc.value})
			if r.Status != 422 || r.Detail() == "" {
				t.Fatal(r)
			}
		})
	}
	for _, tc := range []struct {
		field string
		value string
	}{
		{"hair_texture", strings.Repeat("发", 40)},
		{"preferences", strings.Repeat("喜", 200)},
		{"dislikes", strings.Repeat("忌", 200)},
	} {
		t.Run(tc.field+" at limit", func(t *testing.T) {
			r := s.HTTPWithToken(t, s.Owner, "update_customer_profile", map[string]any{"id": a["id"], tc.field: tc.value})
			if r.Status != 200 || r.Body[tc.field] != tc.value {
				t.Fatal(r)
			}
		})
	}
}

func TestCustomerHairProfileOverview(t *testing.T) {
	s := open(t)
	a := s.create(map[string]any{"name": "概览发质顾客", "phone": "13800000088"})
	s.call("update_customer_profile", map[string]any{
		"id": a["id"], "name": "概览发质顾客", "hair_texture": "细软", "preferences": "不要吹太干", "dislikes": "怕烫",
	}, 200)
	caller := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "list_customer_overview", map[string]any{"query": "概览发质顾客"})
		if r.Status != 200 || r.Body["total"].(float64) != 1 {
			t.Fatal(ch.Name, r)
		}
		item := r.Body["items"].([]any)[0].(map[string]any)
		customer := item["customer"].(map[string]any)
		assertEqual(t, customer["hair_texture"], "细软")
		assertEqual(t, customer["preferences"], "不要吹太干")
		assertEqual(t, customer["dislikes"], "怕烫")
	}
}

func TestCustomerHairProfileMerge(t *testing.T) {
	s := open(t)
	keep := s.create(map[string]any{"name": "保留"})
	merged := s.create(map[string]any{"name": "并入"})
	s.call("update_customer_profile", map[string]any{
		"id": merged["id"], "note": "并入备注",
		"hair_texture": "粗硬", "preferences": "喜欢清爽", "dislikes": "不要染",
	}, 200)
	s.call("merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": merged["id"]}, 200)
	got := s.get(keep["id"])
	assertEqual(t, got["note"], "并入备注")
	assertEqual(t, got["hair_texture"], "粗硬")
	assertEqual(t, got["preferences"], "喜欢清爽")
	assertEqual(t, got["dislikes"], "不要染")
}
