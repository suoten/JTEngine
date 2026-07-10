// Package validation 提供通用 API 输入验证工具。
//
// 用于在 handler 层对用户输入进行统一校验，避免重复代码。
// 支持：手机号、经纬度、分页参数、时间范围、字符串长度等验证。
package validation

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 常见验证正则
var (
	// phoneRegex 匹配中国大陆手机号（1开头的11位数字）
	phoneRegex = regexp.MustCompile(`^1[3-9]\d{9}$`)
	// deviceIDRegex 匹配设备 ID（字母数字下划线连字符，1-64 字符）
	deviceIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	// plateNoRegex 匹配车牌号（支持普通车牌和新能源车牌）
	// 中文省份简称 + 字母 + 5-6位字母数字
	plateNoRegex = regexp.MustCompile(`^[\x{4e00}-\x{9fa5}][A-Za-z][A-Za-z0-9]{4,6}$`)
	// usernameRegex 匹配用户名（字母数字下划线，3-32 字符）
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
)

// ValidatePhone 验证中国大陆手机号格式
func ValidatePhone(phone string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return fmt.Errorf("手机号不能为空")
	}
	if !phoneRegex.MatchString(phone) {
		return fmt.Errorf("手机号格式无效: %s", phone)
	}
	return nil
}

// ValidateDeviceID 验证设备 ID 格式
func ValidateDeviceID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("设备ID不能为空")
	}
	if !deviceIDRegex.MatchString(id) {
		return fmt.Errorf("设备ID格式无效（仅允许字母数字下划线连字符，1-64字符）")
	}
	return nil
}

// ValidatePlateNo 验证车牌号格式
func ValidatePlateNo(plate string) error {
	plate = strings.TrimSpace(plate)
	if plate == "" {
		return fmt.Errorf("车牌号不能为空")
	}
	if !plateNoRegex.MatchString(plate) {
		return fmt.Errorf("车牌号格式无效: %s", plate)
	}
	return nil
}

// ValidateUsername 验证用户名格式
func ValidateUsername(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("用户名不能为空")
	}
	if !usernameRegex.MatchString(username) {
		return fmt.Errorf("用户名格式无效（仅允许字母数字下划线，3-32字符）")
	}
	return nil
}

// ValidateLatitude 验证纬度（-90 到 90）
func ValidateLatitude(lat float64) error {
	if lat < -90 || lat > 90 {
		return fmt.Errorf("纬度超出范围(-90~90): %f", lat)
	}
	if math.IsNaN(lat) || math.IsInf(lat, 0) {
		return fmt.Errorf("纬度值无效: %f", lat)
	}
	return nil
}

// ValidateLongitude 验证经度（-180 到 180）
func ValidateLongitude(lng float64) error {
	if lng < -180 || lng > 180 {
		return fmt.Errorf("经度超出范围(-180~180): %f", lng)
	}
	if math.IsNaN(lng) || math.IsInf(lng, 0) {
		return fmt.Errorf("经度值无效: %f", lng)
	}
	return nil
}

// ValidateCoordinates 同时验证经纬度
func ValidateCoordinates(lat, lng float64) error {
	if err := ValidateLatitude(lat); err != nil {
		return err
	}
	return ValidateLongitude(lng)
}

// ValidateSpeed 验证速度值（0-300 km/h）
func ValidateSpeed(speed float64) error {
	if speed < 0 || speed > 300 {
		return fmt.Errorf("速度超出范围(0-300): %f", speed)
	}
	if math.IsNaN(speed) || math.IsInf(speed, 0) {
		return fmt.Errorf("速度值无效: %f", speed)
	}
	return nil
}

// ValidateDirection 验证方向角（0-359 度）
func ValidateDirection(dir int) error {
	if dir < 0 || dir > 359 {
		return fmt.Errorf("方向角超出范围(0-359): %d", dir)
	}
	return nil
}

// PaginationParams 分页参数
type PaginationParams struct {
	Page     int
	PageSize int
}

// DefaultPageSize 默认每页条数
const DefaultPageSize = 20

// MaxPageSize 最大每页条数
const MaxPageSize = 1000

// ValidatePagination 验证分页参数，返回修正后的值
func ValidatePagination(page, pageSize int) PaginationParams {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return PaginationParams{Page: page, PageSize: pageSize}
}

// ValidateTimeRange 验证时间范围
// start/end 格式: "2006-01-02 15:04:05" 或 "2006-01-02"
func ValidateTimeRange(start, end string) (time.Time, time.Time, error) {
	var startTime, endTime time.Time
	var err error

	if start != "" {
		startTime, err = parseFlexibleTime(start)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("开始时间格式无效: %w", err)
		}
	}

	if end != "" {
		endTime, err = parseFlexibleTime(end)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("结束时间格式无效: %w", err)
		}
	}

	// 如果两者都有值，检查范围
	if !startTime.IsZero() && !endTime.IsZero() {
		if startTime.After(endTime) {
			return time.Time{}, time.Time{}, fmt.Errorf("开始时间不能晚于结束时间")
		}
		// 限制最大查询范围（365天）
		if endTime.Sub(startTime) > 365*24*time.Hour {
			return time.Time{}, time.Time{}, fmt.Errorf("查询时间范围不能超过365天")
		}
	}

	return startTime, endTime, nil
}

// parseFlexibleTime 尝试多种时间格式解析
func parseFlexibleTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}

// ValidateStringLength 验证字符串长度范围
func ValidateStringLength(field, value string, minLen, maxLen int) error {
	length := len(value)
	if length < minLen {
		return fmt.Errorf("%s长度不能少于%d字符", field, minLen)
	}
	if length > maxLen {
		return fmt.Errorf("%s长度不能超过%d字符", field, maxLen)
	}
	return nil
}

// ValidateIP 验证 IP 地址格式
func ValidateIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP地址不能为空")
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("IP地址格式无效: %s", ip)
	}
	return nil
}

// ValidatePort 验证端口号（1-65535）
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口号超出范围(1-65535): %d", port)
	}
	return nil
}

// ValidateIntRange 验证整数范围
func ValidateIntRange(field string, value, min, max int) error {
	if value < min || value > max {
		return fmt.Errorf("%s超出范围(%d-%d): %d", field, min, max, value)
	}
	return nil
}

// ValidateFloatRange 验证浮点数范围
func ValidateFloatRange(field string, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s值无效: %f", field, value)
	}
	if value < min || value > max {
		return fmt.Errorf("%s超出范围(%f-%f): %f", field, min, max, value)
	}
	return nil
}

// SanitizeString 清理字符串输入（去除首尾空格、控制字符）
func SanitizeString(s string) string {
	s = strings.TrimSpace(s)
	// 移除控制字符（保留换行和制表符）
	var b strings.Builder
	for _, r := range s {
		if r >= 32 || r == '\n' || r == '\t' || r == '\r' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ValidatePageSizeFromString 从字符串解析并验证分页大小
func ValidatePageSizeFromString(s string) (int, error) {
	if s == "" {
		return DefaultPageSize, nil
	}
	size, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("分页大小必须是数字: %s", s)
	}
	if size < 1 || size > MaxPageSize {
		return 0, fmt.Errorf("分页大小超出范围(1-%d): %d", MaxPageSize, size)
	}
	return size, nil
}

// ValidatePageFromString 从字符串解析并验证页码
func ValidatePageFromString(s string) (int, error) {
	if s == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("页码必须是数字: %s", s)
	}
	if page < 1 {
		return 0, fmt.Errorf("页码不能小于1: %d", page)
	}
	return page, nil
}
