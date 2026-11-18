package privacy

import (
	"reflect"
	"strings"
	"testing"
)

func TestPhoneKey(t *testing.T) {
	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"phone", true},
		{"customer_phone", true},
		{"need_phone", true},
		{"phones", false},
		{"phone_number", false},
		{"telephone", false},
		{"PHONE", false},
		{"", false},
	} {
		if got := PhoneKey(tt.key); got != tt.want {
			t.Errorf("PhoneKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestPhoneField(t *testing.T) {
	type input struct {
		Phone         string  `json:"phone"`
		CustomerPhone *string `json:"customer_phone"`
		NeedPhone     bool    `json:"need_phone"`
		MobilePhone   string  `json:"contact"`
		PhoneNumber   string  `json:"phone_number"`
		StringPhone   *string `json:"other_phone,string"`
		IgnoredPhone  string  `json:"-"`
	}
	tp := reflect.TypeFor[input]()
	for _, tt := range []struct {
		name  string
		field string
		phone bool
		want  string
	}{
		{"conforming phone", "Phone", true, ""},
		{"conforming suffix", "CustomerPhone", true, ""},
		{"non-string ignored", "NeedPhone", false, ""},
		{"non-conforming key", "MobilePhone", true, `input.MobilePhone has JSON key "contact"`},
		{"non-conforming suffix", "PhoneNumber", true, `input.PhoneNumber has JSON key "phone_number"`},
		{"string option", "StringPhone", true, "uses the JSON string option"},
		{"ignored", "IgnoredPhone", false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			field, ok := tp.FieldByName(tt.field)
			if !ok {
				t.Fatalf("field %s not found", tt.field)
			}
			phone, err := PhoneField(tp, field)
			if phone != tt.phone {
				t.Fatalf("PhoneField(%s) phone = %v, want %v", tt.field, phone, tt.phone)
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("PhoneField(%s) err = %v", tt.field, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("PhoneField(%s) err = %v, want containing %q", tt.field, err, tt.want)
			}
		})
	}
}
