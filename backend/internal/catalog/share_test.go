package catalog_test

import (
	"reflect"
	"testing"
)

func TestPositionShares(t *testing.T) {
	s := open(t)
	p := s.position(true)
	in := input("岗位比例")
	positions := []any{map[string]any{"position_id": p, "required": false, "share_percent": 70}, map[string]any{"position_id": p, "required": false, "share_percent": 30}}
	in["positions"] = positions
	item := s.call("create_item", in, 200)
	for _, share := range []any{nil, 20} {
		positions[0].(map[string]any)["share_percent"] = 70
		if share == nil {
			positions[0].(map[string]any)["share_percent"] = 100
		}
		positions[1].(map[string]any)["share_percent"] = share
		for _, op := range []string{"create_item", "update_item"} {
			args := in
			if op == "update_item" {
				args = update(in, item["id"])
			}
			r := s.call(op, args, 409)
			if r["detail"] != "岗位比例要么都不填，要么都填且合计 100%" {
				t.Fatal(r)
			}
		}
	}
	positions[0].(map[string]any)["share_percent"] = 40
	positions[1].(map[string]any)["share_percent"] = 60
	after := s.call("update_item", update(in, item["id"]), 200)
	for i, want := range []float64{40, 60} {
		if after["positions"].([]any)[i].(map[string]any)["share_percent"] != want {
			t.Fatal(after)
		}
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_items", map[string]any{})
		if r.Status != 200 || r.Body["items"].([]any)[0].(map[string]any)["positions"].([]any)[0].(map[string]any)["share_percent"] != float64(40) {
			t.Fatal(r)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_audit_log", map[string]any{"target_id": item["id"]})
		if r.Status != 200 {
			t.Fatal(r)
		}
		e := r.Body["entries"].([]any)[0].(map[string]any)
		if e["before"].(map[string]any)["positions"].([]any)[0].(map[string]any)["share_percent"] != float64(70) || e["after"].(map[string]any)["positions"].([]any)[0].(map[string]any)["share_percent"] != float64(40) {
			t.Fatal(e)
		}
	}
	for _, op := range []string{"create_item", "update_item"} {
		args := in
		if op == "update_item" {
			args = update(in, item["id"])
		}
		before := s.call("list_items", map[string]any{}, 200)
		r := s.AI(t, s.MustCaller(s.Owner), op, args)
		if r.Status != 409 || r.Detail() != "operation requires a draft confirmed by a person" {
			t.Fatal(r)
		}
		if !reflect.DeepEqual(before, s.call("list_items", map[string]any{}, 200)) {
			t.Fatal("AI changed data")
		}
	}
}
