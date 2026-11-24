package ai

import (
	"net/http"
	"os"
	"time"
)

// OpenAIConfigFromEnv 从 AI_API_KEY、AI_BASE_URL、AI_MODEL、AI_EXTRA_HEADERS 构造 OpenAI 配置。
func OpenAIConfigFromEnv() (OpenAIConfig, error) {
	headers, err := ParseExtraHeaders(os.Getenv("AI_EXTRA_HEADERS"))
	if err != nil {
		return OpenAIConfig{}, err
	}
	return OpenAIConfig{
		BaseURL:    os.Getenv("AI_BASE_URL"),
		APIKey:     os.Getenv("AI_API_KEY"),
		Model:      os.Getenv("AI_MODEL"),
		HTTPClient: newHTTPClient(),
		Headers:    headers,
	}, nil
}

// newHTTPClient 构造 AI 请求专用的 HTTP 客户端。
// 代理会在连接空闲约 5 分钟后悄悄断开隧道，复用这条连接的下一次请求会一直挂到超时；
// 因此提前丢弃空闲连接，并开启 HTTP/2 ping 健康检查，让已失效但仍在复用的连接被发现并关闭。
func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.IdleConnTimeout = 60 * time.Second
	transport.HTTP2 = &http.HTTP2Config{
		SendPingTimeout: 15 * time.Second,
		PingTimeout:     10 * time.Second,
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: transport}
}
