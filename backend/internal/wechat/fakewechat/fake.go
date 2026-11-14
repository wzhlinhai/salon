// Package fakewechat 提供仅供开发和测试使用的微信登录假实现。
package fakewechat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/wzhlinhai/salon/backend/internal/wechat"
)

var phonePattern = regexp.MustCompile(`^1[0-9]{10}$`)

// Fake 从约定格式的 code 读取 openid 和手机号。
type Fake struct{}

// New 返回内存微信登录假实现。
func New() *Fake { return &Fake{} }

// Session 把 fake-openid:<openid> 解析为 openid。
func (*Fake) Session(_ context.Context, code string) (wechat.Session, error) {
	openid, ok := strings.CutPrefix(code, "fake-openid:")
	if !ok || openid == "" {
		return wechat.Session{}, errors.New("fakewechat: invalid code")
	}
	return wechat.Session{OpenID: openid}, nil
}

// PhoneNumber 把 fake-phone:<11 位手机号> 解析为手机号。
func (*Fake) PhoneNumber(_ context.Context, phoneCode string) (string, error) {
	phone, ok := strings.CutPrefix(phoneCode, "fake-phone:")
	if !ok || !phonePattern.MatchString(phone) {
		return "", fmt.Errorf("fakewechat: invalid phone code")
	}
	return phone, nil
}
