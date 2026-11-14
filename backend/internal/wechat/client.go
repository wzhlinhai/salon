package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultBaseURL = "https://api.weixin.qq.com"

// Config 是微信官方接口的配置。
type Config struct {
	AppID      string
	Secret     string
	BaseURL    string
	HTTPClient *http.Client
	Now        func() time.Time
}

// Client 通过微信官方 HTTP 接口登录并读取手机号。
type Client struct {
	appID   string
	secret  string
	baseURL string
	http    *http.Client
	now     func() time.Time

	tokenMu      sync.Mutex
	accessToken  string
	tokenExpires time.Time
}

// NewClient 返回微信官方接口客户端。AppID 和 Secret 必填。
func NewClient(cfg Config) (*Client, error) {
	if cfg.AppID == "" || cfg.Secret == "" {
		return nil, fmt.Errorf("wechat: APPID and SECRET are required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		return nil, fmt.Errorf("wechat: Now is required")
	}
	return &Client{appID: cfg.AppID, secret: cfg.Secret, baseURL: cfg.BaseURL, http: cfg.HTTPClient, now: cfg.Now}, nil
}

type sessionResponse struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// Session 用小程序登录 code 换取 openid。
func (c *Client) Session(ctx context.Context, code string) (Session, error) {
	query := url.Values{}
	query.Set("appid", c.appID)
	query.Set("secret", c.secret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")
	var out sessionResponse
	if err := c.getJSON(ctx, "jscode2session", "/sns/jscode2session?"+query.Encode(), &out); err != nil {
		return Session{}, err
	}
	if out.ErrCode != 0 {
		return Session{}, &APIError{Op: "jscode2session", ErrCode: out.ErrCode, ErrMsg: out.ErrMsg}
	}
	if out.OpenID == "" {
		return Session{}, ErrInvalidResponse
	}
	return Session{OpenID: out.OpenID, UnionID: out.UnionID}, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

func (c *Client) token(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken != "" && c.now().Before(c.tokenExpires) {
		return c.accessToken, nil
	}
	query := url.Values{}
	query.Set("grant_type", "client_credential")
	query.Set("appid", c.appID)
	query.Set("secret", c.secret)
	var out tokenResponse
	if err := c.getJSON(ctx, "token", "/cgi-bin/token?"+query.Encode(), &out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 {
		return "", &APIError{Op: "token", ErrCode: out.ErrCode, ErrMsg: out.ErrMsg}
	}
	if out.AccessToken == "" || out.ExpiresIn <= 0 {
		return "", ErrInvalidResponse
	}
	margin := time.Duration(out.ExpiresIn) * time.Second / 10
	if margin > 5*time.Minute {
		margin = 5 * time.Minute
	}
	c.accessToken = out.AccessToken
	c.tokenExpires = c.now().Add(time.Duration(out.ExpiresIn)*time.Second - margin)
	return c.accessToken, nil
}

func (c *Client) invalidateToken() {
	c.tokenMu.Lock()
	c.accessToken = ""
	c.tokenExpires = time.Time{}
	c.tokenMu.Unlock()
}

type phoneResponse struct {
	ErrCode   int    `json:"errcode"`
	ErrMsg    string `json:"errmsg"`
	PhoneInfo struct {
		PhoneNumber string `json:"phone_number"`
	} `json:"phone_info"`
}

// PhoneNumber 用手机号 code 读取微信绑定的手机号。
func (c *Client) PhoneNumber(ctx context.Context, phoneCode string) (string, error) {
	for attempt := 1; ; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return "", err
		}
		body, _ := json.Marshal(map[string]string{"code": phoneCode})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/wxa/business/getuserphonenumber?access_token="+url.QueryEscape(token), bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("wechat: phone request")
		}
		req.Header.Set("Content-Type", "application/json")
		var out phoneResponse
		if err := c.doJSON(req, "getuserphonenumber", &out); err != nil {
			return "", err
		}
		if out.ErrCode != 0 {
			if attempt == 1 && (out.ErrCode == 40001 || out.ErrCode == 40014 || out.ErrCode == 42001) {
				c.invalidateToken()
				continue
			}
			return "", &APIError{Op: "getuserphonenumber", ErrCode: out.ErrCode, ErrMsg: out.ErrMsg}
		}
		if out.PhoneInfo.PhoneNumber == "" {
			return "", ErrInvalidResponse
		}
		return out.PhoneInfo.PhoneNumber, nil
	}
}

func (c *Client) getJSON(ctx context.Context, op, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("wechat: %s request", op)
	}
	return c.doJSON(req, op, out)
}

func (c *Client) doJSON(req *http.Request, op string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// 不包装底层错误：它的文字可能包含带 secret 或 access_token 的 URL。
		return fmt.Errorf("wechat: %s request failed", op)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wechat: %s status %d", op, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("wechat: %s read response", op)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("wechat: %s decode response", op)
	}
	return nil
}
