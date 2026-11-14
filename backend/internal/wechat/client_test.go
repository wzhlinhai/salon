package wechat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/wechat"
)

const testSecret = "test-secret-never-log"

func TestClientSessionAndPhone(t *testing.T) {
	var tokens atomic.Int32
	var phoneCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			tokens.Add(1)
			if r.URL.Query().Get("appid") != "app" || r.URL.Query().Get("secret") != testSecret {
				t.Errorf("token query = %v", r.URL.Query())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-1", "expires_in": 7200})
		case "/sns/jscode2session":
			if r.URL.Query().Get("js_code") != "login-code" {
				t.Errorf("js_code = %q", r.URL.Query().Get("js_code"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"openid": "openid-1", "unionid": "union-1", "session_key": "session-secret"})
		case "/wxa/business/getuserphonenumber":
			phoneCalls.Add(1)
			if r.URL.Query().Get("access_token") != "token-1" {
				t.Errorf("access_token = %q", r.URL.Query().Get("access_token"))
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["code"] != "phone-code" {
				t.Errorf("phone body = %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "phone_info": map[string]string{"phone_number": "13800138000"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := wechat.NewClient(wechat.Config{AppID: "app", Secret: testSecret, BaseURL: server.URL, HTTPClient: server.Client(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Session(context.Background(), "login-code")
	if err != nil || session.OpenID != "openid-1" || session.UnionID != "union-1" {
		t.Fatalf("session = %+v, %v", session, err)
	}
	for range 2 {
		phone, err := client.PhoneNumber(context.Background(), "phone-code")
		if err != nil || phone != "13800138000" {
			t.Fatalf("phone = %q, %v", phone, err)
		}
	}
	if tokens.Load() != 1 || phoneCalls.Load() != 2 {
		t.Fatalf("token calls = %d, phone calls = %d; want token cached", tokens.Load(), phoneCalls.Load())
	}
}

func TestClientRefreshesExpiredToken(t *testing.T) {
	var tokens atomic.Int32
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			n := tokens.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-" + string(rune('0'+n)), "expires_in": 7200})
		case "/wxa/business/getuserphonenumber":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "phone_info": map[string]string{"phone_number": "13800138000"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := wechat.NewClient(wechat.Config{AppID: "app", Secret: testSecret, BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PhoneNumber(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := client.PhoneNumber(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	if tokens.Load() != 2 {
		t.Fatalf("token calls = %d, want refreshed", tokens.Load())
	}
}

func TestClientRetriesInvalidToken(t *testing.T) {
	for _, code := range []int{40001, 40014, 42001} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var tokenCalls, phoneCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/token":
					n := tokenCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-" + string(rune('0'+n)), "expires_in": 7200})
				case "/wxa/business/getuserphonenumber":
					if phoneCalls.Add(1) == 1 {
						_ = json.NewEncoder(w).Encode(map[string]any{"errcode": code, "errmsg": "invalid credential"})
						return
					}
					if r.URL.Query().Get("access_token") != "token-2" {
						t.Errorf("retry token = %q", r.URL.Query().Get("access_token"))
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "phone_info": map[string]string{"phone_number": "13800138000"}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := wechat.NewClient(wechat.Config{AppID: "app", Secret: testSecret, BaseURL: server.URL, HTTPClient: server.Client(), Now: time.Now})
			if err != nil {
				t.Fatal(err)
			}
			phone, err := client.PhoneNumber(context.Background(), "c")
			if err != nil || phone != "13800138000" {
				t.Fatalf("phone = %q, %v", phone, err)
			}
			if tokenCalls.Load() != 2 || phoneCalls.Load() != 2 {
				t.Fatalf("token calls = %d, phone calls = %d", tokenCalls.Load(), phoneCalls.Load())
			}
		})
	}
}

func TestClientAPIErrorAndNoSecretLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 40029, "errmsg": "invalid code"})
	}))
	defer server.Close()
	client, err := wechat.NewClient(wechat.Config{AppID: "app", Secret: testSecret, BaseURL: server.URL, HTTPClient: server.Client(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Session(context.Background(), "bad")
	var api *wechat.APIError
	if !errors.As(err, &api) || api.ErrCode != 40029 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testSecret) || strings.Contains(err.Error(), "session_key") {
		t.Fatalf("error leaks secret: %v", err)
	}

	bad, err := wechat.NewClient(wechat.Config{AppID: "app", Secret: testSecret, BaseURL: "http://127.0.0.1:1", HTTPClient: &http.Client{Timeout: time.Second}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Session(context.Background(), "code"); err == nil || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("transport error leaks secret: %v", err)
	}
}
