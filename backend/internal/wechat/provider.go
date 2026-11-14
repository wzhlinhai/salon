// Package wechat 定义微信小程序登录适配协议和官方接口实现。
package wechat

import (
	"context"
	"errors"
)

// Session 是微信登录换取的用户标识。
type Session struct {
	OpenID  string
	UnionID string
}

// Provider 是微信小程序登录与会话的手机号读取协议。
type Provider interface {
	Session(ctx context.Context, code string) (Session, error)
	PhoneNumber(ctx context.Context, phoneCode string) (string, error)
}

// APIError 是微信接口返回的 errcode 错误，不包含密钥、令牌或 session_key。
type APIError struct {
	Op      string
	ErrCode int
	ErrMsg  string
}

func (e *APIError) Error() string {
	return "wechat: " + e.Op + " errcode " + itoa(e.ErrCode) + ": " + e.ErrMsg
}

// ErrInvalidResponse 表示微信接口响应缺少必要字段。
var ErrInvalidResponse = errors.New("wechat: invalid response")

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
