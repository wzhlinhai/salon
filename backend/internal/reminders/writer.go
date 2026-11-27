package reminders

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wzhlinhai/salon/backend/internal/ai"
)

const (
	// writerMaxRunes 是模型写的话最多保留的字符数，超过就改用固定文案。
	writerMaxRunes = 120
)

// writerTimeout 是一次写话调用的期限；测试可以缩短。
var writerTimeout = 15 * time.Second

var numberPattern = regexp.MustCompile(`[0-9][0-9,]*(?:\.[0-9]+)?|\.[0-9]+`)

// chineseQuantityPattern 拦住“两笔”“三万元”“十次”这类中文数字加量词；不拦“一两句”这种不以量词结尾的用法。
var chineseQuantityPattern = regexp.MustCompile(`[零〇一二两三四五六七八九十百千万亿]+[笔次元块个位天日号月年条单张]`)

const writerSystemPrompt = "只把给定事实写成一两句给收件人看的中文提醒；数字一律用阿拉伯数字；不加事实里没有的数字和判断；不写人名和手机号；不超过 80 字。"

// GenerateText 让模型把事实写成提醒；模型出错、没配、超时、为空、过长、用中文数字加量词或出现事实里没有的数字时返回 false。
func GenerateText(ctx context.Context, model ai.Model, rule Rule, facts json.RawMessage) (string, bool) {
	if model == nil || len(facts) == 0 {
		return "", false
	}
	allowed, ok := factNumbers(facts)
	if !ok {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, writerTimeout)
	defer cancel()
	message, err := model.Chat(ctx, []ai.Message{
		{Role: ai.RoleSystem, Content: writerSystemPrompt},
		{Role: ai.RoleUser, Content: "规则：" + rule.Name + "\n事实：" + string(facts)},
	}, nil)
	if err != nil || ctx.Err() != nil {
		return "", false
	}
	text := strings.TrimSpace(message.Content)
	if text == "" || utf8.RuneCountInString(text) > writerMaxRunes {
		return "", false
	}
	if chineseQuantityPattern.MatchString(text) {
		return "", false
	}
	for _, number := range textNumbers(text) {
		if !allowed[number] {
			return "", false
		}
	}
	return text, true
}

func factNumbers(raw json.RawMessage) (map[string]bool, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	out := map[string]bool{}
	collectFactNumbers(value, out)
	return out, true
}

func collectFactNumbers(value any, out map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			collectFactNumbers(child, out)
		}
	case []any:
		for _, child := range v {
			collectFactNumbers(child, out)
		}
	case string:
		addNumbers(v, out)
	case json.Number:
		addNumbers(v.String(), out)
	case float64:
		addNumbers(strconv.FormatFloat(v, 'f', -1, 64), out)
	}
}

func textNumbers(text string) []string {
	out := []string{}
	for _, number := range extractNumbers(text) {
		out = append(out, normalizeNumber(number))
	}
	return out
}

func addNumbers(text string, out map[string]bool) {
	for _, number := range extractNumbers(text) {
		out[normalizeNumber(number)] = true
	}
}

func extractNumbers(text string) []string {
	normalized := normalizeDigits(text)
	return numberPattern.FindAllString(normalized, -1)
}

func normalizeDigits(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= '０' && r <= '９':
			b.WriteRune('0' + r - '０')
		case r == '．':
			b.WriteRune('.')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeNumber(number string) string {
	number = strings.ReplaceAll(number, ",", "")
	integer, fraction, hasFraction := strings.Cut(number, ".")
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	if !hasFraction {
		return integer
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return integer
	}
	return integer + "." + fraction
}
