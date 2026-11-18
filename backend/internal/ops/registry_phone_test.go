package ops_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type phoneOut struct{}

func registerPhoneOp[I any](t *testing.T, ai ops.AIMode) {
	t.Helper()
	ops.Register(newRegistry(t), ops.Operation[I, phoneOut]{
		Name: "phone_input_probe", Summary: "手机号字段检查", Kind: ops.Write, Permission: "thing.edit", AI: ai,
		Handle: func(context.Context, ops.Env, *I) (*phoneOut, error) { return &phoneOut{}, nil },
	})
}

type phoneDirect struct {
	MobilePhone string `json:"contact"`
}

type phoneNested struct {
	Inner phoneDirect `json:"inner"`
}

type phoneSlice struct {
	Items []phoneDirect `json:"items"`
}

type phoneEmbedded struct {
	phoneDirect
}

type phonePointer struct {
	Item *phoneDirect `json:"item"`
}

type phoneConforming struct {
	Phone         string  `json:"phone"`
	CustomerPhone *string `json:"customer_phone"`
	NeedPhone     bool    `json:"need_phone"`
}

type phoneDynamic struct {
	Value any `json:"value"`
}

type phoneIgnored struct {
	Cache *phoneDirect `json:"-"`
}

type phonePrivate struct {
	cache *phoneDirect
}

func TestAIDraftPhoneKeysRejectedAtRegistration(t *testing.T) {
	for _, tt := range []struct {
		name     string
		register func(*testing.T)
		want     string
	}{
		{"direct", func(t *testing.T) { registerPhoneOp[phoneDirect](t, ops.AIDraft) }, `phoneDirect.MobilePhone has JSON key "contact"`},
		{"nested", func(t *testing.T) { registerPhoneOp[phoneNested](t, ops.AIDraft) }, `phoneDirect.MobilePhone has JSON key "contact"`},
		{"slice", func(t *testing.T) { registerPhoneOp[phoneSlice](t, ops.AIDraft) }, `phoneDirect.MobilePhone has JSON key "contact"`},
		{"embedded", func(t *testing.T) { registerPhoneOp[phoneEmbedded](t, ops.AIDraft) }, `phoneDirect.MobilePhone has JSON key "contact"`},
		{"pointer", func(t *testing.T) { registerPhoneOp[phonePointer](t, ops.AIDraft) }, `phoneDirect.MobilePhone has JSON key "contact"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, tt.want, func() { tt.register(t) })
		})
	}
}

func TestAIDraftPhoneKeysAcceptConformingAndDynamicFields(t *testing.T) {
	for _, tt := range []struct {
		name     string
		register func(*testing.T)
	}{
		{"conforming", func(t *testing.T) { registerPhoneOp[phoneConforming](t, ops.AIDraft) }},
		{"dynamic interface", func(t *testing.T) { registerPhoneOp[phoneDynamic](t, ops.AIDraft) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.register(t)
		})
	}
}

func TestAIDraftPhoneKeysIgnoreUnserialisedFields(t *testing.T) {
	_ = phonePrivate{cache: &phoneDirect{}}
	registerPhoneOp[phoneIgnored](t, ops.AIDraft)
	registerPhoneOp[phonePrivate](t, ops.AIDraft)
}

func TestAINonePhoneKeysNotChecked(t *testing.T) {
	registerPhoneOp[phoneDirect](t, ops.AINone)
}
