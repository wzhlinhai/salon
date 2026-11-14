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
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Headers:    headers,
	}, nil
}
