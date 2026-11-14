package ai_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ai"
)

func TestParseExtraHeaders(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		want       map[string]string
		wantErr    string
		wantNoName string
	}{
		{"empty", "", nil, "", ""},
		{"whitespace", " \n", nil, "", ""},
		{"valid", `{"x-opencode-session":"salon"}`, map[string]string{"X-Opencode-Session": "salon"}, "", ""},
		{"mixed case name", `{"X-Custom-Header":"value"}`, map[string]string{"X-Custom-Header": "value"}, "", ""},
		{"invalid json", `{"x":"secret-value"`, nil, "必须是 JSON 对象", ""},
		{"not object", `["secret-value"]`, nil, "必须是 JSON 对象", ""},
		{"non string", `{"x":"secret-value","n":1}`, nil, "值必须是字符串", ""},
		{"empty name", `{"":"secret-value"}`, nil, "请求头名", ""},
		{"bad name", `{"bad name":"secret-value"}`, nil, "请求头名", ""},
		{"bad value", `{"x":"secret-value\u0001"}`, nil, "请求头值", ""},
		{"authorization", `{"Authorization":"Bearer secret-value"}`, nil, "不能设置 Authorization 或 Content-Type", ""},
		{"content type", `{"content-type":"secret-value"}`, nil, "不能设置 Authorization 或 Content-Type", ""},
		{"duplicate", `{"x-foo":"secret-value","x-foo":"other"}`, nil, "不能包含重复的请求头名", "x-foo"},
		{"duplicate case", `{"X-Foo":"secret-value","x-foo":"other"}`, nil, "不能包含重复的请求头名", "x-foo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers, err := ai.ParseExtraHeaders(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), "secret-value") {
					t.Fatalf("err = %v, must not echo header values", err)
				}
				if tc.wantNoName != "" && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantNoName)) {
					t.Fatalf("err = %v, must not echo header names", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(headers, tc.want) {
				t.Fatalf("headers = %#v, want %#v", headers, tc.want)
			}
		})
	}
}
