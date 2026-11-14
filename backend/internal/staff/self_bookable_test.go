package staff_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestStaffSelfBookable(t *testing.T) {
	s := open(t)
	created := s.call(s.Owner, "create_staff", map[string]any{"name": "可约员工", "number": "002", "role_ids": []uuid.UUID{}, "self_bookable": true}, http.StatusOK)
	id := uuid.MustParse(created.Body["id"].(string))
	if created.Body["self_bookable"] != true {
		t.Fatalf("created self_bookable = %v", created.Body["self_bookable"])
	}
	kept := s.call(s.Owner, "update_staff", map[string]any{"staff_id": id, "name": "可约员工", "number": "002"}, http.StatusOK)
	if kept.Body["self_bookable"] != true {
		t.Fatalf("omitted self_bookable changed the value: %v", kept.Body["self_bookable"])
	}
	off := s.call(s.Owner, "update_staff", map[string]any{"staff_id": id, "name": "可约员工", "number": "002", "self_bookable": false}, http.StatusOK)
	if off.Body["self_bookable"] != false {
		t.Fatalf("updated self_bookable = %v", off.Body["self_bookable"])
	}
	list := s.call(s.Owner, "list_staff", map[string]any{}, http.StatusOK)
	found := false
	for _, raw := range list.Body["staff"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == id.String() {
			found = true
			if row["self_bookable"] != false {
				t.Fatalf("list self_bookable = %v", row["self_bookable"])
			}
		}
	}
	if !found {
		t.Fatal("updated staff missing from list")
	}
	sawCreate, sawOn, sawOff := false, false, false
	logs := s.call(s.Owner, "list_audit_log", map[string]any{"target_id": id}, http.StatusOK)
	for _, raw := range logs.Body["entries"].([]any) {
		entry := raw.(map[string]any)
		action := entry["action"].(string)
		if action != "create_staff" && action != "update_staff" {
			continue
		}
		after, ok := entry["after"].(map[string]any)
		if !ok {
			continue
		}
		if action == "create_staff" && after["self_bookable"] == true {
			sawCreate = true
		}
		if action == "update_staff" && after["self_bookable"] == true {
			sawOn = true
		}
		if action == "update_staff" && after["self_bookable"] == false {
			sawOff = true
		}
	}
	if !sawCreate || !sawOn || !sawOff {
		t.Fatalf("self_bookable audit missing: create=%v on=%v off=%v", sawCreate, sawOn, sawOff)
	}
}
