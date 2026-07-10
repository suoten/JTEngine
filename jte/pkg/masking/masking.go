// Package masking 提供敏感数据脱敏工具（等保2.0 三级 - 数据脱敏要求）。
//
// 支持脱敏类型：
//   - 手机号：138****8000
//   - 身份证号：110101****1234
//   - 车牌号：京A***45
//   - 邮箱：z***@example.com
//   - 姓名：张*（两字）/ 张*三（三字及以上）
package masking

import (
	"strings"
	"unicode/utf8"
)

// MaskPhone 脱敏手机号：保留前 3 后 4，中间 4 位 *
// 13800138000 → 138****8000
// 长度不足 7 位则全部脱敏为 ****
func MaskPhone(phone string) string {
	if len(phone) <= 0 {
		return ""
	}
	// 去除可能的国际区号前缀
	cleaned := strings.TrimPrefix(phone, "+86")
	cleaned = strings.TrimPrefix(cleaned, "86")
	if len(cleaned) < 7 {
		return strings.Repeat("*", len(cleaned))
	}
	return cleaned[:3] + "****" + cleaned[len(cleaned)-4:]
}

// MaskIDCard 脱敏身份证号：保留前 6 后 4，中间 *
// 110101199001011234 → 110101********1234
// 长度不足 10 位则全部脱敏
func MaskIDCard(idCard string) string {
	if len(idCard) <= 0 {
		return ""
	}
	cleaned := strings.ToUpper(strings.TrimSpace(idCard))
	if len(cleaned) < 10 {
		return strings.Repeat("*", len(cleaned))
	}
	return cleaned[:6] + strings.Repeat("*", len(cleaned)-10) + cleaned[len(cleaned)-4:]
}

// MaskPlate 脱敏车牌号：保留首 2 位和末 2 位，中间 *
// 京A12345 → 京A***45
// 京A8 → 京A8（长度不足不脱敏）
func MaskPlate(plate string) string {
	if len(plate) <= 0 {
		return ""
	}
	cleaned := strings.ToUpper(strings.TrimSpace(plate))
	// 车牌至少 2 位（省份+字母）才脱敏
	if utf8.RuneCountInString(cleaned) < 4 {
		return cleaned
	}
	// 按 rune 处理（中文省份是 3 字节）
	runes := []rune(cleaned)
	if len(runes) < 4 {
		return cleaned
	}
	return string(runes[:2]) + strings.Repeat("*", len(runes)-4) + string(runes[len(runes)-2:])
}

// MaskEmail 脱敏邮箱：保留首字符和 @ 后域名
// zhangsan@example.com → z***@example.com
func MaskEmail(email string) string {
	if len(email) <= 0 {
		return ""
	}
	at := strings.Index(email, "@")
	if at <= 0 || at >= len(email)-1 {
		return strings.Repeat("*", len(email))
	}
	name := email[:at]
	domain := email[at:]
	if len(name) <= 1 {
		return name + "***" + domain
	}
	return string(name[0]) + "***" + domain
}

// MaskName 脱敏姓名：保留首字，其余 *
// 张 → 张
// 张三 → 张*
// 张三丰 → 张*丰
// 诸葛孔明 → 诸**明
func MaskName(name string) string {
	if len(name) <= 0 {
		return ""
	}
	runes := []rune(name)
	switch len(runes) {
	case 0:
		return ""
	case 1:
		return string(runes)
	case 2:
		return string(runes[0]) + "*"
	default:
		// 首字 + (n-2)个* + 末字
		return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
	}
}

// MaskByType 按类型脱敏（便捷方法）
// 支持类型：phone / id_card / plate / email / name
func MaskByType(maskType, value string) string {
	switch maskType {
	case "phone":
		return MaskPhone(value)
	case "id_card":
		return MaskIDCard(value)
	case "plate":
		return MaskPlate(value)
	case "email":
		return MaskEmail(value)
	case "name":
		return MaskName(value)
	default:
		return value
	}
}

// IsSensitiveField 判断字段名是否为敏感字段（需脱敏）
// 字段名匹配规则：不区分大小写，支持常见命名（phone/mobile/id_card/idcard/plate/license/email/name）
func IsSensitiveField(fieldName string) (bool, string) {
	if fieldName == "" {
		return false, ""
	}
	lower := strings.ToLower(fieldName)
	switch {
	case strings.Contains(lower, "phone") || strings.Contains(lower, "mobile") || strings.Contains(lower, "tel"):
		return true, "phone"
	case strings.Contains(lower, "id_card") || strings.Contains(lower, "idcard") || strings.Contains(lower, "id_number") || strings.Contains(lower, "idnumber") || strings.Contains(lower, "identity"):
		return true, "id_card"
	case strings.Contains(lower, "plate") || strings.Contains(lower, "license") || strings.Contains(lower, "licenseplate") || strings.Contains(lower, "car_no"):
		return true, "plate"
	case strings.Contains(lower, "email") || strings.Contains(lower, "mail"):
		return true, "email"
	case lower == "name" || lower == "driver_name" || lower == "owner_name" || strings.HasSuffix(lower, "_name"):
		return true, "name"
	default:
		return false, ""
	}
}
