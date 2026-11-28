package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestDatabasePoolUsesStoreTimeZone(t *testing.T) {
	previous := time.Local
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	time.Local = losAngeles
	t.Cleanup(func() { time.Local = previous })
	t.Setenv("DATABASE_URL", dbtest.DSN(t))
	pool, err := openDatabase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var scanned time.Time
	if err := pool.QueryRow(context.Background(), "SELECT '2026-11-11T12:00:00+08:00'::timestamptz").Scan(&scanned); err != nil {
		t.Fatal(err)
	}
	if got := scanned.Format(time.RFC3339); got != "2026-11-11T12:00:00+08:00" {
		t.Fatalf("scanned time = %s, want 2026-11-11T12:00:00+08:00", got)
	}
}

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

func TestAIAgentEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		model bool
		want  bool
	}{
		{"on", true, true},
		{"on", false, false},
		{"off", true, false},
		{"", true, false},
		{"ON", true, false},
		{"other", true, false},
	} {
		if got := aiAgentEnabled(tc.value, tc.model); got != tc.want {
			t.Fatalf("aiAgentEnabled(%q, %v) = %v, want %v", tc.value, tc.model, got, tc.want)
		}
	}
}
