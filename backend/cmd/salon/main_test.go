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

func TestWechatLoginProviderSelection(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		t.Setenv("WECHAT_APPID", "")
		t.Setenv("WECHAT_SECRET", "")
		t.Setenv("WECHAT_FAKE", "")
		provider, err := wechatLoginProvider()
		if err != nil || provider != nil {
			t.Fatalf("provider = %v, err = %v", provider, err)
		}
	})
	t.Run("fake", func(t *testing.T) {
		t.Setenv("WECHAT_APPID", "")
		t.Setenv("WECHAT_SECRET", "")
		t.Setenv("WECHAT_FAKE", "1")
		provider, err := wechatLoginProvider()
		if err != nil || provider == nil {
			t.Fatalf("provider = %v, err = %v", provider, err)
		}
	})
	t.Run("real", func(t *testing.T) {
		t.Setenv("WECHAT_APPID", "app")
		t.Setenv("WECHAT_SECRET", "secret")
		t.Setenv("WECHAT_FAKE", "")
		provider, err := wechatLoginProvider()
		if err != nil || provider == nil {
			t.Fatalf("provider = %v, err = %v", provider, err)
		}
	})
	t.Run("both", func(t *testing.T) {
		t.Setenv("WECHAT_APPID", "app")
		t.Setenv("WECHAT_SECRET", "secret")
		t.Setenv("WECHAT_FAKE", "1")
		if _, err := wechatLoginProvider(); err == nil {
			t.Fatal("want refusal with both fake and real")
		}
	})
	t.Run("incomplete real", func(t *testing.T) {
		t.Setenv("WECHAT_APPID", "app")
		t.Setenv("WECHAT_SECRET", "")
		t.Setenv("WECHAT_FAKE", "")
		if _, err := wechatLoginProvider(); err == nil {
			t.Fatal("want refusal with only appid")
		}
	})
}
