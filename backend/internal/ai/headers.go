package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/textproto"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// ParseExtraHeaders 解析 AI_EXTRA_HEADERS 的 JSON 对象；空值返回 nil，Authorization 和 Content-Type 不允许覆盖。
func ParseExtraHeaders(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
	}
	headers := map[string]string{}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
		}
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("AI_EXTRA_HEADERS 的值必须是字符串")
		}
		if !httpguts.ValidHeaderFieldName(name) {
			return nil, errors.New("AI_EXTRA_HEADERS 包含无效的请求头名")
		}
		if !httpguts.ValidHeaderFieldValue(text) {
			return nil, errors.New("AI_EXTRA_HEADERS 包含无效的请求头值")
		}
		if forbiddenHeader(name) {
			return nil, errors.New("AI_EXTRA_HEADERS 不能设置 Authorization 或 Content-Type")
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if seen[canonical] {
			return nil, errors.New("AI_EXTRA_HEADERS 不能包含重复的请求头名")
		}
		seen[canonical] = true
		headers[canonical] = text
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("AI_EXTRA_HEADERS 必须是 JSON 对象")
	}
	return headers, nil
}

func forbiddenHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Content-Type")
}
