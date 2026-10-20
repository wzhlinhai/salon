// Package privacy 提供返回业务数据时共用的隐私处理。
package privacy

// MaskPhone 将十一位数字号码打码，其他字符串保持原样。
func MaskPhone(phone string) string {
	if len(phone) != 11 {
		return phone
	}
	for _, digit := range phone {
		if digit < '0' || digit > '9' {
			return phone
		}
	}
	return phone[:3] + "****" + phone[7:]
}
