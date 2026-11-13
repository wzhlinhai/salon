package main

import (
	"reflect"
	"testing"
)

func TestFakeGroupbuyRegistry(t *testing.T) {
	providers, platforms := fakeGroupbuy("")
	if len(platforms) != 0 || providers.Get("meituan") != nil {
		t.Fatal("unset GROUPBUY_FAKE_PLATFORMS must register nothing")
	}
	providers, platforms = fakeGroupbuy(" meituan, douyin,meituan,")
	if !reflect.DeepEqual(platforms, []string{"meituan", "douyin"}) {
		t.Fatal(platforms)
	}
	if providers.Get("meituan") == nil || providers.Get("douyin") == nil || providers.Get("other") != nil {
		t.Fatal("registered platforms do not match")
	}
}
