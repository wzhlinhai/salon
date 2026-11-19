// Package privacy 提供返回业务数据时共用的隐私处理。
package privacy

import (
	"fmt"
	"reflect"
	"strings"
)

// MaskName 保留姓名第一个字，其余每个字替换为 *；空姓名返回空串。
func MaskName(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-1)
}

// MaskPhone 先按 normalizePhone 的规则去掉空白和短横线；得到十一位数字时保留前三位和后四位，
// 否则把输入中的每个 ASCII 数字替换为 *。
func MaskPhone(phone string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, phone)
	if len(cleaned) == 11 {
		digits := true
		for _, r := range cleaned {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			return cleaned[:3] + "****" + cleaned[7:]
		}
	}
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '*'
		}
		return r
	}, phone)
}

// PhoneKey 报告 JSON 键名是否承载手机号：键名为 phone 或以 _phone 结尾。
func PhoneKey(key string) bool {
	return key == "phone" || strings.HasSuffix(key, "_phone")
}

// PhoneKeyword 报告键名是否像手机号字段名；供无法读取字段名的动态键使用。
func PhoneKeyword(key string) bool {
	return strings.Contains(strings.ToLower(key), "phone")
}

// PhoneField 判断结构体字段是否参与序列化，并检查其中的手机号字段。
// 返回 false 表示字段不会进入日志或草稿，调用者不应检查也不应递归。
func PhoneField(t reflect.Type, field reflect.StructField) (bool, error) {
	fieldType := field.Type
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	if !field.IsExported() && (!field.Anonymous || fieldType.Kind() != reflect.Struct) {
		return false, nil
	}
	tag := field.Tag.Get("json")
	if tag == "-" {
		return false, nil
	}
	key, options, _ := strings.Cut(tag, ",")
	if key == "" {
		key = field.Name
	}
	if !PhoneKeyword(field.Name) || fieldType.Kind() != reflect.String {
		return true, nil
	}
	if !PhoneKey(key) {
		return true, fmt.Errorf("privacy: field %s.%s has JSON key %q; phone numbers must use the key \"phone\" or a key ending in \"_phone\" so they can be masked", t.Name(), field.Name, key)
	}
	if strings.Contains(","+options+",", ",string,") {
		return true, fmt.Errorf("privacy: field %s.%s has JSON key %q and uses the JSON string option; quoted phone numbers cannot be masked", t.Name(), field.Name, key)
	}
	return true, nil
}
