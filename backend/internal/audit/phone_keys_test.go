package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestCheckPhoneKeys(t *testing.T) {
	type Order struct {
		CustomerPhone string `json:"customer_phone"`
	}
	type PointerOrder struct {
		CustomerPhone *string `json:"customer_phone"`
	}
	type Embedded struct {
		Order
	}
	type node struct{ Next *node }
	n := node{}
	n.Next = &n
	var cyclic any
	cyclic = &cyclic
	var deep any = "leaf"
	for range 65 {
		child := deep
		deep = &child
	}
	phone := "13800131234"
	for _, tt := range []struct {
		name  string
		value any
		want  string
	}{
		{"depth limit", deep, "audit: value nested too deeply"},
		{"self reference", &n, "audit: value nested too deeply"},
		{"interface cycle", cyclic, "audit: value nested too deeply"},
		{"map interface cycle", map[string]any{"phone": cyclic}, "audit: value nested too deeply"},
		{"string option", struct {
			Phone string `json:"phone,string"`
		}{phone}, "JSON string option; quoted phone numbers cannot be masked"},
		{"pointer string option", struct {
			Phone *string `json:"phone,string,omitempty"`
		}{&phone}, "JSON string option; quoted phone numbers cannot be masked"},
		{"omitempty option", struct {
			Phone string `json:"phone,omitempty"`
		}{phone}, ""},
		{"map pointer", map[string]any{"phone_number": &phone}, `map key "phone_number"`},
		{"map phone pointer", map[string]any{"phone": &phone}, ""},
		{"map nil pointer", map[string]any{"phone_number": (*string)(nil)}, ""},
		{"string", Order{phone}, `Order.CustomerPhone is logged as "customer_phone"`},
		{"pointer", PointerOrder{&phone}, `PointerOrder.CustomerPhone is logged as "customer_phone"`},
		{"nil phone pointer", PointerOrder{}, `PointerOrder.CustomerPhone is logged as "customer_phone"`},
		{"slice", []Order{{phone}}, `Order.CustomerPhone is logged as "customer_phone"`},
		{"array", [1]Order{{phone}}, `Order.CustomerPhone is logged as "customer_phone"`},
		{"interface", struct{ Value any }{Order{phone}}, `Order.CustomerPhone is logged as "customer_phone"`},
		{"embedded", Embedded{Order{phone}}, `Order.CustomerPhone is logged as "customer_phone"`},
		{"map", map[string]any{"phone_number": phone}, `map key "phone_number"`},
		{"map case", map[string]any{"PHONE": phone}, `map key "PHONE"`},
		{"map nested", map[string]any{"order": &Order{phone}}, `Order.CustomerPhone is logged as "customer_phone"`},
		{"default field name", struct{ Phone string }{phone}, `.Phone is logged as "Phone"`},
		{"tag options", struct {
			Phone string `json:"phone_number,omitempty"`
		}{phone}, `.Phone is logged as "phone_number"`},
		{"phone", struct {
			Phone *string `json:"phone"`
		}{&phone}, ""},
		{"masked flag", struct {
			PhoneMasked bool `json:"phone_masked"`
		}{true}, ""},
		{"nil", nil, ""},
		{"nil pointer", (*Order)(nil), ""},
		{"map phone", map[string]any{"phone": phone}, ""},
		{"map flag", map[string]any{"phone_masked": true}, ""},
		{"map nil", map[string]any{"phone_number": nil}, ""},
		{"ordinary", struct {
			Name string `json:"name"`
		}{"顾客"}, ""},
		{"ignored", struct {
			Phone string `json:"-"`
		}{phone}, ""},
		{"unexported", struct{ phone string }{phone}, ""},
	} {
		if !t.Run(tt.name, func(t *testing.T) {
			err := checkPhoneKeys(tt.value)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("checkPhoneKeys() = %v, want error containing %q", err, tt.want)
			}
		}) {
			t.FailNow()
		}
	}
}

func TestRecordRejectsPhoneKeysBeforeDatabase(t *testing.T) {
	invalid := struct {
		CustomerPhone string `json:"customer_phone"`
	}{"13800131234"}
	for _, tt := range []struct {
		name          string
		before, after any
	}{
		{"before", invalid, nil},
		{"after", nil, invalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("Record reached the database before rejecting phone keys: %v", p)
				}
			}()
			err := Record(context.Background(), ops.Env{}, "test", StaffTarget(uuid.Nil), tt.before, tt.after)
			if err == nil || !strings.Contains(err.Error(), `CustomerPhone is logged as "customer_phone"`) {
				t.Fatalf("Record() = %v, want phone key error", err)
			}
		})
	}
}
